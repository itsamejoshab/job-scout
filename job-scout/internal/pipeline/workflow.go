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
)

// ManualScrapeWorkflowID returns a unique operator scrape ID that cannot collide
// with the reserved scheduled ID.
func ManualScrapeWorkflowID(now time.Time) string {
	return manualScrapeIDPrefix + now.UTC().Format(time.RFC3339Nano)
}

// ScrapeTick fetches LinkedIn search results and stores jobs. It does not filter
// or notify.
func ScrapeTick(ctx workflow.Context, input scraper.TickInput) (scraper.Result, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("ScrapeTick starting", "force", input.Force, "jobSource", input.JobSource)

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	})

	var a *Activities
	var result scraper.Result
	if err := workflow.ExecuteActivity(ctx, a.Scrape, input).Get(ctx, &result); err != nil {
		logger.Error("Scrape activity failed", "err", err)
		return scraper.Result{Status: "error", JobSource: input.JobSource, Error: err.Error()}, nil
	}

	logger.Info("ScrapeTick complete", "result", result)
	return result, nil
}

// NotifyTick filters pending jobs to eligible, claims a batch, and POSTs once.
func NotifyTick(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("NotifyTick starting")

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

	var a *Activities
	var snap NotifySnapshot
	if err := workflow.ExecuteActivity(dbCtx, a.LoadNotifySnapshot).Get(ctx, &snap); err != nil {
		return err
	}

	for _, job := range snap.Pending {
		d := domain.FilterPending(job, snap.All, snap.Lists)
		if d.NeedFetch {
			var desc string
			err := workflow.ExecuteActivity(httpCtx, a.FetchJobDescription, job.JobURL).Get(ctx, &desc)
			if err != nil {
				d = domain.OnDetailFetchFailure(job)
			} else {
				job.Description = desc
				d = domain.FilterAfterDescription(job, snap.Lists)
			}
		}
		if err := workflow.ExecuteActivity(dbCtx, a.ApplyJobDecision, ApplyJobDecisionInput{
			JobID:    job.ID,
			Decision: d,
		}).Get(ctx, nil); err != nil {
			return err
		}
	}

	var batch ClaimBatch
	if err := workflow.ExecuteActivity(dbCtx, a.ClaimNotifyBatch).Get(ctx, &batch); err != nil {
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
	postErr := workflow.ExecuteActivity(httpCtx, a.PostHomeAssistant, msg).Get(ctx, nil)
	state := domain.StateNotified
	if postErr != nil {
		state = domain.StateEligible
	}
	if err := workflow.ExecuteActivity(dbCtx, a.FinishNotifyBatch, FinishNotifyBatchInput{
		IDs:   ids,
		State: state,
	}).Get(ctx, nil); err != nil {
		return err
	}

	logger.Info("NotifyTick complete")
	return nil
}
