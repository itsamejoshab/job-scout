package pipeline

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/domain"
	"github.com/jobscout/jobscout/internal/scraper"
)

// Activities holds dependencies for pipeline activities. Registering the struct
// with the worker registers all of its methods as activities.
type Activities struct {
	Scraper                   *scraper.Service
	DB                        *sql.DB
	Webhook                   *WebhookClient
	Temporal                  SignalStarter
	NotifyMaxJobs             int
	NotifyClaimTimeoutSeconds int
}

// wake_process_pending signals or starts the pending dispatcher singleton.
// Workflows cannot signal-with-start, so they call this activity.
func (a *Activities) wake_process_pending(ctx context.Context) error {
	return WakeProcessPending(ctx, a.Temporal)
}

// scrape_jobs fetches due (or forced) enabled providers and persists jobs.
func (a *Activities) scrape_jobs(ctx context.Context, input scraper.TickInput) (scraper.Result, error) {
	return a.Scraper.RunTick(ctx, input)
}

// load_next_pending_job returns the oldest pending row not skipped by this run.
func (a *Activities) load_next_pending_job(ctx context.Context, input LoadNextPendingInput) (int64, error) {
	return db.NextPendingJobID(ctx, a.DB, input.SkipIDs)
}

// load_process_job loads the current row before any processing decision.
func (a *Activities) load_process_job(ctx context.Context, jobID int64) (ProcessJob, error) {
	row, err := db.GetJob(ctx, a.DB, jobID)
	if err != nil {
		return ProcessJob{}, err
	}
	return processJobFromDB(row), nil
}

// load_duplicate_group loads title+company peers without filtering by state.
func (a *Activities) load_duplicate_group(ctx context.Context, _ DuplicateGroupInput) ([]domain.Job, error) {
	rows, err := db.ListDuplicateCandidates(ctx, a.DB)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Job, len(rows))
	for i, row := range rows {
		out[i] = domainJobFromDB(row)
	}
	return out, nil
}

// load_filter_lists reads the current word lists for one processing decision.
func (a *Activities) load_filter_lists(ctx context.Context) (domain.Lists, error) {
	settings, err := db.GetSearchSettings(ctx, a.DB)
	if err != nil {
		return domain.Lists{}, err
	}
	if settings == nil {
		return domain.Lists{}, nil
	}
	return domain.Lists{
		TitleInclude: settings.TitleInclude, TitleExclude: settings.TitleExclude,
		CompanyExclude: settings.CompanyExclude, DescInclude: settings.DescIncludeWords,
		DescExclude: settings.DescExcludeWords,
	}, nil
}

// get_job_description holds the source lock only while it GETs LinkedIn detail.
func (a *Activities) get_job_description(ctx context.Context, in DetailFetchInput) (DetailFetchResult, error) {
	conn, err := a.DB.Conn(ctx)
	if err != nil {
		return DetailFetchResult{}, err
	}
	defer conn.Close()

	source := db.JobSource(in.JobSource)
	locked, err := db.TryLockJobSource(ctx, conn, source)
	if err != nil {
		return DetailFetchResult{}, err
	}
	if !locked {
		return DetailFetchResult{LockBusy: true}, nil
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := db.UnlockJobSource(unlockCtx, conn, source); err != nil {
			slog.Error("job detail advisory unlock failed; discarding connection", "source", source, "err", err)
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()

	description, err := a.Scraper.FetchJobDescription(ctx, in.JobURL)
	if err != nil || strings.TrimSpace(description) == "" {
		return DetailFetchResult{FetchFailed: true}, nil
	}
	return DetailFetchResult{Description: description}, nil
}

// save_job_filter_result persists one filter decision. It does not claim or notify.
func (a *Activities) save_job_filter_result(ctx context.Context, in ApplyJobDecisionInput) error {
	var reason *string
	if in.Decision.RejectReason != "" {
		r := in.Decision.RejectReason
		reason = &r
	}
	var desc *string
	if persistDescription(in.Decision) {
		d := in.Decision.Description
		desc = &d
	}
	return db.UpdateJobNotifyState(ctx, a.DB, in.JobID, in.Decision.State, reason, desc, in.Decision.DetailAttempts)
}

// ClaimedJob is one row taken for this webhook POST.
type ClaimedJob struct {
	ID     int64
	JobURL string
}

// ClaimBatch is the claimed ready jobs.
type ClaimBatch struct {
	Jobs []ClaimedJob
}

// FinishNotifyBatchInput identifies a failed batch whose markers must clear.
type FinishNotifyBatchInput struct {
	IDs []int64
}

// claim_notification_batch claims up to NOTIFY_MAX_JOBS oldest ready jobs.
func (a *Activities) claim_notification_batch(ctx context.Context) (ClaimBatch, error) {
	limit := a.NotifyMaxJobs
	if limit <= 0 {
		limit = 25
	}
	jobs, err := db.ClaimReadyJobs(ctx, a.DB, limit, a.NotifyClaimTimeoutSeconds)
	if err != nil {
		return ClaimBatch{}, err
	}
	out := ClaimBatch{
		Jobs: make([]ClaimedJob, len(jobs)),
	}
	for i, j := range jobs {
		out.Jobs[i] = ClaimedJob{ID: j.ID, JobURL: j.JobURL}
		slog.Debug(
			"job included in alert batch",
			"job_id", j.ID,
			"title", j.Title,
			"company", j.Company,
			"location", j.Location,
			"search_context", j.SearchContext,
			"job_url", j.JobURL,
		)
	}
	return out, nil
}

// send_notification POSTs the lead message once. MaximumAttempts must be 1.
func (a *Activities) send_notification(ctx context.Context, message string) error {
	if a.Webhook == nil {
		return fmt.Errorf("webhook client is not configured")
	}
	return a.Webhook.PostMessage(ctx, message)
}

// finish_notification_batch clears dedupe markers after a failed POST.
func (a *Activities) finish_notification_batch(ctx context.Context, in FinishNotifyBatchInput) error {
	return db.ClearNotifyClaims(ctx, a.DB, in.IDs)
}

func persistDescription(d domain.Decision) bool {
	if d.Description != "" {
		return true
	}
	return d.State == domain.StateReady ||
		d.RejectReason == domain.ReasonDescription
}

func processJobFromDB(row db.Job) ProcessJob {
	return ProcessJob{
		Job:       domainJobFromDB(row),
		State:     row.State,
		JobSource: string(row.JobSource),
	}
}

func domainJobFromDB(row db.Job) domain.Job {
	description := ""
	if row.Description != nil {
		description = *row.Description
	}
	return domain.Job{
		ID: row.ID, Title: row.Title, Company: row.Company,
		Description: description, JobURL: row.JobURL,
		CreatedAt: row.CreatedAt, DetailAttempts: row.DetailAttempts,
	}
}
