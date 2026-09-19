package pipeline

import (
	"math/rand"
	"time"

	"github.com/jobscout/jobscout/internal/domain"
	"github.com/jobscout/jobscout/internal/scraper"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	// ScheduledScrapeWorkflowID is the reserved ID for the scrape schedule.
	ScheduledScrapeWorkflowID = "jobscout-scrape-scheduled"
	ProcessPendingWorkflowID  = "jobscout-process-pending"
	manualScrapeIDPrefix      = "jobscout-scrape-manual-"

	// SignalJobsAvailable wakes the idle dispatcher. It carries no payload.
	SignalJobsAvailable = "JobsAvailable"

	ActivityScrapeJobs              = "scrape_jobs"
	ActivityWakeProcessPending      = "wake_process_pending"
	ActivityLoadNextPendingJob      = "load_next_pending_job"
	ActivityLoadJobsForFiltering    = "load_jobs_for_filtering"
	ActivityLoadProcessJob          = "load_process_job"
	ActivityLoadDuplicateGroup      = "load_duplicate_group"
	ActivityLoadFilterLists         = "load_filter_lists"
	ActivityGetJobDescription       = "get_job_description"
	ActivitySaveJobFilterResult     = "save_job_filter_result"
	ActivityClaimNotificationBatch  = "claim_notification_batch"
	ActivitySendNotification        = "send_notification"
	ActivityFinishNotificationBatch = "finish_notification_batch"
)

const (
	processJobLockRetryDelay  = 15 * time.Second
	processJobLockMaxRetries  = 8
	processPendingThrottle    = 60 * time.Second
	processPendingMaxJitter   = 30 * time.Second
	processPendingIdleWait    = 2 * time.Minute
	processPendingMaxChildren = 50
)

// ProcessJob is the row data needed by ProcessJobWorkflow.
type ProcessJob struct {
	Job       domain.Job
	State     string
	JobSource string
}

// ProcessJobResult tells the parent if this child used a source HTTP attempt.
type ProcessJobResult struct {
	Throttle bool
}

// LoadNextPendingInput holds job IDs that this workflow run must not retry.
type LoadNextPendingInput struct {
	SkipIDs []int64
}

// DuplicateGroupInput identifies rows compared for duplicate winner selection.
type DuplicateGroupInput struct {
	Title   string
	Company string
}

// DetailFetchInput identifies one provider detail request.
type DetailFetchInput struct {
	JobURL    string
	JobSource string
}

// DetailFetchResult separates lock contention from a failed provider response.
type DetailFetchResult struct {
	Description string
	LockBusy    bool
	FetchFailed bool
}

type enricher interface {
	DetailActivity() string
}

type linkedInEnricher struct{}

func (linkedInEnricher) DetailActivity() string { return ActivityGetJobDescription }

func selectEnricher(jobSource string) (enricher, bool) {
	if jobSource == "LINKEDIN" {
		return linkedInEnricher{}, true
	}
	return nil, false
}

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
		result = scraper.Result{Status: "error", JobSource: input.JobSource, Error: err.Error()}
	}

	// Wake the dispatcher for every outcome: an error or skipped scrape can
	// still leave pending rows from an earlier run.
	wakeCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})
	if err := workflow.ExecuteActivity(wakeCtx, ActivityWakeProcessPending).Get(ctx, nil); err != nil {
		logger.Error("Wake process-pending failed", "err", err)
	}

	logger.Info("ScrapeWorkflow complete", "result", result)
	return result, nil
}

// ProcessPendingWorkflow serially drains pending rows and rolls its history
// after 50 children or one idle wait.
func ProcessPendingWorkflow(ctx workflow.Context) error {
	dbCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})
	skipIDs := make([]int64, 0)

	for childrenStarted := 0; childrenStarted < processPendingMaxChildren; childrenStarted++ {
		var jobID int64
		if err := workflow.ExecuteActivity(
			dbCtx,
			ActivityLoadNextPendingJob,
			LoadNextPendingInput{SkipIDs: skipIDs},
		).Get(ctx, &jobID); err != nil {
			return err
		}
		if jobID == 0 {
			if err := waitForJobsAvailable(ctx); err != nil {
				return err
			}
			return workflow.NewContinueAsNewError(ctx, ProcessPendingWorkflow)
		}

		var result ProcessJobResult
		childErr := workflow.ExecuteChildWorkflow(ctx, ProcessJobWorkflow, jobID).Get(ctx, &result)
		if childErr != nil {
			skipIDs = append(skipIDs, jobID)
			result.Throttle = true
		}
		if result.Throttle {
			if err := workflow.Sleep(ctx, processPendingThrottle+processPendingJitter(ctx)); err != nil {
				return err
			}
		}
	}

	return workflow.NewContinueAsNewError(ctx, ProcessPendingWorkflow)
}

func processPendingJitter(ctx workflow.Context) time.Duration {
	encoded := workflow.SideEffect(ctx, func(workflow.Context) any {
		return time.Duration(rand.Intn(int(processPendingMaxJitter/time.Second)+1)) * time.Second
	})
	var jitter time.Duration
	if err := encoded.Get(&jitter); err != nil {
		panic(err)
	}
	return jitter
}

func waitForJobsAvailable(ctx workflow.Context) error {
	signal := workflow.GetSignalChannel(ctx, SignalJobsAvailable)
	timerCtx, cancelTimer := workflow.WithCancel(ctx)
	timer := workflow.NewTimer(timerCtx, processPendingIdleWait)
	selector := workflow.NewSelector(ctx)
	var waitErr error
	selector.AddReceive(signal, func(ch workflow.ReceiveChannel, _ bool) {
		ch.Receive(ctx, nil)
		cancelTimer()
	})
	selector.AddFuture(timer, func(f workflow.Future) {
		waitErr = f.Get(ctx, nil)
	})
	selector.Select(ctx)
	return waitErr
}

// ProcessJobWorkflow moves one pending row to ready or rejected.
func ProcessJobWorkflow(ctx workflow.Context, jobID int64) (ProcessJobResult, error) {
	dbCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})
	getCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	})

	var loaded ProcessJob
	if err := workflow.ExecuteActivity(dbCtx, ActivityLoadProcessJob, jobID).Get(ctx, &loaded); err != nil {
		return ProcessJobResult{}, err
	}
	if loaded.State != domain.StatePending {
		return ProcessJobResult{}, nil
	}

	var group []domain.Job
	if err := workflow.ExecuteActivity(dbCtx, ActivityLoadDuplicateGroup, DuplicateGroupInput{
		Title: loaded.Job.Title, Company: loaded.Job.Company,
	}).Get(ctx, &group); err != nil {
		return ProcessJobResult{}, err
	}
	if domain.IsDuplicateLoser(loaded.Job, group) {
		return saveProcessDecision(ctx, dbCtx, loaded.Job.ID, domain.Decision{
			State: domain.StateRejected, RejectReason: domain.ReasonDuplicate,
			DetailAttempts: loaded.Job.DetailAttempts,
		})
	}

	var lists domain.Lists
	if err := workflow.ExecuteActivity(dbCtx, ActivityLoadFilterLists).Get(ctx, &lists); err != nil {
		return ProcessJobResult{}, err
	}
	decision := domain.FilterPending(loaded.Job, group, lists)
	if !decision.NeedFetch {
		return saveProcessDecision(ctx, dbCtx, loaded.Job.ID, decision)
	}

	selected, ok := selectEnricher(loaded.JobSource)
	if !ok {
		return saveProcessDecision(ctx, dbCtx, loaded.Job.ID, domain.Decision{
			State: domain.StateRejected, RejectReason: domain.ReasonUnsupportedSource,
			DetailAttempts: loaded.Job.DetailAttempts,
		})
	}

	for attempt := 0; attempt < processJobLockMaxRetries; attempt++ {
		var fetched DetailFetchResult
		err := workflow.ExecuteActivity(getCtx, selected.DetailActivity(), DetailFetchInput{
			JobURL: loaded.Job.JobURL, JobSource: loaded.JobSource,
		}).Get(ctx, &fetched)
		if err != nil {
			return ProcessJobResult{}, err
		}
		if fetched.LockBusy {
			if attempt+1 < processJobLockMaxRetries {
				if err := workflow.Sleep(ctx, processJobLockRetryDelay); err != nil {
					return ProcessJobResult{}, err
				}
			}
			continue
		}
		if fetched.FetchFailed || fetched.Description == "" {
			result, err := saveProcessDecision(ctx, dbCtx, loaded.Job.ID, domain.OnDetailFetchFailure(loaded.Job))
			result.Throttle = true
			return result, err
		}

		loaded.Job.Description = fetched.Description
		result, err := saveProcessDecision(
			ctx, dbCtx, loaded.Job.ID, domain.FilterAfterDescription(loaded.Job, lists),
		)
		result.Throttle = true
		return result, err
	}

	return ProcessJobResult{}, nil
}

func saveProcessDecision(
	ctx workflow.Context,
	dbCtx workflow.Context,
	jobID int64,
	decision domain.Decision,
) (ProcessJobResult, error) {
	err := workflow.ExecuteActivity(dbCtx, ActivitySaveJobFilterResult, ApplyJobDecisionInput{
		JobID: jobID, Decision: decision,
	}).Get(ctx, nil)
	return ProcessJobResult{}, err
}

// NotifyWorkflow claims ready jobs and POSTs one notification.
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
	msg := domain.BuildReadyMessage(urls)
	postErr := workflow.ExecuteActivity(httpCtx, ActivitySendNotification, msg).Get(ctx, nil)
	if postErr != nil {
		if err := workflow.ExecuteActivity(dbCtx, ActivityFinishNotificationBatch, FinishNotifyBatchInput{
			IDs: ids,
		}).Get(ctx, nil); err != nil {
			return err
		}
		return postErr
	}

	logger.Info("NotifyWorkflow complete")
	return nil
}
