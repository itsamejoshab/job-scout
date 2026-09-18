package pipeline

import (
	"context"
	"database/sql"
	"fmt"

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

// Scrape fetches due (or forced) enabled providers and persists jobs.
func (a *Activities) Scrape(ctx context.Context, input scraper.TickInput) (scraper.Result, error) {
	return a.Scraper.RunTick(ctx, input)
}

// LoadNotifySnapshot reads live filter lists and all jobs.
func (a *Activities) LoadNotifySnapshot(ctx context.Context) (NotifySnapshot, error) {
	return LoadNotifySnapshot(ctx, a.DB)
}

// FetchJobDescription GETs LinkedIn job-detail HTML once.
func (a *Activities) FetchJobDescription(ctx context.Context, jobURL string) (string, error) {
	return a.Scraper.FetchJobDescription(ctx, jobURL)
}

// ApplyJobDecision persists one filter decision. It does not claim or notify.
func (a *Activities) ApplyJobDecision(ctx context.Context, in ApplyJobDecisionInput) error {
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

// ClaimedJob is one row taken for this Home Assistant POST.
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

// ClaimNotifyBatch claims up to NOTIFY_MAX_JOBS oldest eligible jobs.
func (a *Activities) ClaimNotifyBatch(ctx context.Context) (ClaimBatch, error) {
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
			RemoteLie:    inv.RemoteLie,
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
	}
	return out, nil
}

// PostHomeAssistant POSTs the lead message once. MaximumAttempts must be 1.
func (a *Activities) PostHomeAssistant(ctx context.Context, message string) error {
	if a.Webhook == nil {
		return fmt.Errorf("webhook client is not configured")
	}
	return a.Webhook.PostMessage(ctx, message)
}

// FinishNotifyBatch writes notified after HTTP 200, or eligible after failure.
func (a *Activities) FinishNotifyBatch(ctx context.Context, in FinishNotifyBatchInput) error {
	return db.SetJobsState(ctx, a.DB, in.IDs, in.State)
}

func persistDescription(d domain.Decision) bool {
	if d.Description != "" {
		return true
	}
	return d.State == domain.StateEligible ||
		d.RejectReason == domain.ReasonDescription ||
		d.RejectReason == domain.ReasonRemoteLie
}
