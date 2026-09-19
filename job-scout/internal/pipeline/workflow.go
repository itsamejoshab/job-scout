package pipeline

import (
	"time"

	"github.com/jobscout/jobscout/internal/domain"
	"github.com/jobscout/jobscout/internal/scraper"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	// ScheduledScrapeWorkflowID is the reserved ID for the scrape schedule.
	ScheduledScrapeWorkflowID = "jobscout-scrape-scheduled"
	manualScrapeIDPrefix      = "jobscout-scrape-manual-"

	ActivityScrapeJobs              = "scrape_jobs"
	ActivityLoadJobsForFiltering    = "load_jobs_for_filtering"
	ActivityGetJobDescription       = "get_job_description"
	ActivitySaveJobFilterResult     = "save_job_filter_result"
	ActivityClaimNotificationBatch  = "claim_notification_batch"
	ActivitySendNotification        = "send_notification"
	ActivityFinishNotificationBatch = "finish_notification_batch"
)

// ManualScrapeWorkflowID returns a unique operator scrape ID that cannot collide
// with the reserved scheduled ID.
func ManualScrapeWorkflowID(now time.Time) string {
	return manualScrapeIDPrefix + now.UTC().Format(time.RFC3339Nano)
}

// ScrapeWorkflow fetches provider search results and stores distinct jobs. It
// does not filter or notify.
func ScrapeWorkflow(ctx workflow.Context, input scraper.TickInput) (scraper.Result, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("ScrapeWorkflow starting", "force", input.Force, "jobSource", input.JobSource)

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 45 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	})

	var result scraper.Result
	if err := workflow.ExecuteActivity(ctx, ActivityScrapeJobs, input).Get(ctx, &result); err != nil {
		logger.Error("Scrape activity failed", "err", err)
		return scraper.Result{Status: "error", JobSource: input.JobSource, Error: err.Error()}, nil
	}

	logger.Info("ScrapeWorkflow complete", "result", result)
	return result, nil
}

// NotifyWorkflow filters pending jobs to eligible, claims a batch, and POSTs
// one notification.
func NotifyWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("NotifyWorkflow starting")

	dbCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})
	httpCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	})

	var snap NotifySnapshot
	if err := workflow.ExecuteActivity(dbCtx, ActivityLoadJobsForFiltering).Get(ctx, &snap); err != nil {
		return err
	}

	for _, job := range snap.Pending {
		d := domain.FilterPending(job, snap.All, snap.Lists)
		if d.NeedFetch {
			var desc string
			err := workflow.ExecuteActivity(httpCtx, ActivityGetJobDescription, job.JobURL).Get(ctx, &desc)
			if err != nil {
				d = domain.OnDetailFetchFailure(job)
			} else {
				job.Description = desc
				d = domain.FilterAfterDescription(job, snap.Lists)
			}
		}
		if err := workflow.ExecuteActivity(dbCtx, ActivitySaveJobFilterResult, ApplyJobDecisionInput{
			JobID:    job.ID,
			Decision: d,
		}).Get(ctx, nil); err != nil {
			return err
		}
	}

	var batch ClaimBatch
	if err := workflow.ExecuteActivity(dbCtx, ActivityClaimNotificationBatch).Get(ctx, &batch); err != nil {
		return err
	}
	if len(batch.Jobs) == 0 {
		logger.Info("No leads.")
		return nil
	}

	urls := make([]string, len(batch.Jobs))
	ids := make([]int64, len(batch.Jobs))
	for i, j := range batch.Jobs {
		urls[i] = j.JobURL
		ids[i] = j.ID
	}
	msg := domain.BuildMessage(batch.Counts, urls)
	postErr := workflow.ExecuteActivity(httpCtx, ActivitySendNotification, msg).Get(ctx, nil)
	state := domain.StateNotified
	if postErr != nil {
		state = domain.StateEligible
	}
	if err := workflow.ExecuteActivity(dbCtx, ActivityFinishNotificationBatch, FinishNotifyBatchInput{
		IDs:   ids,
		State: state,
	}).Get(ctx, nil); err != nil {
		return err
	}
	if postErr != nil {
		return postErr
	}

	logger.Info("NotifyWorkflow complete")
	return nil
}
