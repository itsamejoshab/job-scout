package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/domain"
	"github.com/jobscout/jobscout/internal/scraper"
)

// Activities holds dependencies for pipeline activities. Registering the struct
// with the worker registers all of its methods as activities.
type Activities struct {
	Scraper       *scraper.Service
	DB            *sql.DB
	Webhook       *WebhookClient
	NotifyMaxJobs int
}

// scrape_jobs fetches due (or forced) enabled providers and persists jobs.
func (a *Activities) scrape_jobs(ctx context.Context, input scraper.TickInput) (scraper.Result, error) {
	return a.Scraper.RunTick(ctx, input)
}

// load_jobs_for_filtering reads live filter lists and all jobs.
func (a *Activities) load_jobs_for_filtering(ctx context.Context) (NotifySnapshot, error) {
	return LoadNotifySnapshot(ctx, a.DB)
}

// get_job_description GETs LinkedIn job-detail HTML once.
func (a *Activities) get_job_description(ctx context.Context, jobURL string) (string, error) {
	return a.Scraper.FetchJobDescription(ctx, jobURL)
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

// ClaimBatch is the claimed jobs plus inventory counts after claim.
type ClaimBatch struct {
	Jobs   []ClaimedJob
	Counts domain.MessageCounts
}

// FinishNotifyBatchInput sets the claimed batch to notified or eligible.
type FinishNotifyBatchInput struct {
	IDs   []int64
	State string
}

// claim_notification_batch claims up to NOTIFY_MAX_JOBS oldest eligible jobs.
func (a *Activities) claim_notification_batch(ctx context.Context) (ClaimBatch, error) {
	limit := a.NotifyMaxJobs
	if limit <= 0 {
		limit = 25
	}
	jobs, err := db.ClaimEligibleJobs(ctx, a.DB, limit)
	if err != nil {
		return ClaimBatch{}, err
	}
	inv, err := db.LoadNotifyInventory(ctx, a.DB)
	if err != nil {
		return ClaimBatch{}, err
	}
	out := ClaimBatch{
		Jobs: make([]ClaimedJob, len(jobs)),
		Counts: domain.MessageCounts{
			Total:        inv.Total,
			TitleCompany: inv.TitleCompany,
			Description:  inv.Description,
			Duplicate:    inv.Duplicate,
			DetailFailed: inv.DetailFailed,
			Pending:      inv.Pending,
			Eligible:     inv.Eligible,
			Notifying:    inv.Notifying,
			Notified:     inv.Notified,
		},
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

// finish_notification_batch writes notified after HTTP 200, or eligible after failure.
func (a *Activities) finish_notification_batch(ctx context.Context, in FinishNotifyBatchInput) error {
	return db.SetJobsState(ctx, a.DB, in.IDs, in.State)
}

func persistDescription(d domain.Decision) bool {
	if d.Description != "" {
		return true
	}
	return d.State == domain.StateEligible ||
		d.RejectReason == domain.ReasonDescription
}
