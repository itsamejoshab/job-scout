package pipeline

import (
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/domain"
	"github.com/jobscout/jobscout/internal/scraper"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	// ScheduledScrapeWorkflowID is the reserved ID for the scrape schedule.
	ScheduledScrapeWorkflowID = "scrape-scheduled"
	FilterPendingWorkflowID   = "filter-pending"
	ProcessPendingWorkflowID  = "process-pending"
	// LegacyProcessPendingWorkflowID is the pre-rename singleton. API boot
	// terminates it so old and new dispatchers cannot overlap.
	LegacyProcessPendingWorkflowID = "jobscout-process-pending"
	manualScrapeIDPrefix           = "scrape-manual-"
	processJobIDPrefix             = "scrape-more-details-"

	// SignalJobsAvailable wakes an idle dispatcher. It carries no payload.
	SignalJobsAvailable = "JobsAvailable"

	ActivityListDueProviders        = "list_due_providers"
	ActivityScrapeProviderLinkedIn  = "scrape_provider_linkedin"
	ActivityScrapeProviderDice      = "scrape_provider_dice"
	ActivityScrapeProviderIndeed    = "scrape_provider_indeed"
	ActivityWakeFilterPending       = "wake_filter_pending"
	ActivityWakeProcessPending      = "wake_process_pending"
	ActivityLoadNextPendingJob      = "load_next_pending_job"
	ActivityLoadNextNeedsDetailJob  = "load_next_needs_detail_job"
	ActivityFilterPendingJob        = "filter_pending_job"
	ActivityLoadProcessJob          = "load_process_job"
	ActivityLoadFilterLists         = "load_filter_lists"
	ActivityGetJobDescription       = "get_job_description"
	ActivitySaveJobFilterResult     = "save_job_filter_result"
	ActivityClaimNotificationBatch  = "claim_notification_batch"
	ActivitySendNotification        = "send_notification"
	ActivityFinishNotificationBatch = "finish_notification_batch"
	ActivityNotificationStatus      = "notification_status"
)

const (
	processJobLockRetryDelay  = 15 * time.Second
	processJobLockMaxRetries  = 8
	processPendingThrottle    = 60 * time.Second
	processPendingMaxJitter   = 30 * time.Second
	processPendingIdleWait    = 2 * time.Minute
	processPendingMaxChildren = 50
	filterPendingMaxJobs      = 50
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

// FilterPendingJobResult reports whether fast filtering staged a detail fetch.
type FilterPendingJobResult struct {
	WroteNeedsDetail bool
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

// ScrapeProviderTaskQueue selects the serial activity queue for one provider.
func ScrapeProviderTaskQueue(source string) string {
	switch source {
	case "LINKEDIN":
		return config.LinkedInScrapeTaskQueue
	case "DICE", "INDEED":
		return config.ApifyScrapeTaskQueue
	default:
		return config.TaskQueue
	}
}

// ScrapeProviderActivityName returns the Temporal activity type for one provider.
func ScrapeProviderActivityName(source string) string {
	switch source {
	case "LINKEDIN":
		return ActivityScrapeProviderLinkedIn
	case "DICE":
		return ActivityScrapeProviderDice
	case "INDEED":
		return ActivityScrapeProviderIndeed
	default:
		return ActivityScrapeProviderLinkedIn
	}
}

// ProcessJobWorkflowID returns the child workflow ID for one detail fetch.
func ProcessJobWorkflowID(jobID int64) string {
	return fmt.Sprintf("%s%d", processJobIDPrefix, jobID)
}

// ScrapeWorkflow resolves due providers, scrapes each as a parallel activity
// on a provider task queue, then wakes the fast filter. One provider failure
// does not cancel the others.
func ScrapeWorkflow(ctx workflow.Context, input scraper.TickInput) (scraper.Result, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("ScrapeWorkflow starting", "force", input.Force, "jobSource", input.JobSource)

	listCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})
	var sources []string
	if err := workflow.ExecuteActivity(listCtx, ActivityListDueProviders, input).Get(ctx, &sources); err != nil {
		logger.Error("List due providers failed", "err", err)
		result := scraper.Result{Status: "error", JobSource: input.JobSource, Error: err.Error()}
		wakeFilterPending(ctx, logger)
		return result, nil
	}

	result := scraper.Result{Status: "skipped", JobSource: input.JobSource}
	if len(sources) > 0 {
		type scrapeFuture struct {
			source string
			future workflow.Future
		}
		futures := make([]scrapeFuture, 0, len(sources))
		for _, source := range sources {
			actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
				TaskQueue:           ScrapeProviderTaskQueue(source),
				StartToCloseTimeout: 45 * time.Minute,
				// Scrape loops and Apify polls emit heartbeats; fail if stalled.
				HeartbeatTimeout: 2 * time.Minute,
				RetryPolicy: &temporal.RetryPolicy{
					MaximumAttempts: 1,
				},
			})
			futures = append(futures, scrapeFuture{
				source: source,
				future: workflow.ExecuteActivity(actCtx, ScrapeProviderActivityName(source), source),
			})
		}

		results := make([]scraper.Result, 0, len(futures))
		for _, item := range futures {
			var providerResult scraper.Result
			if err := item.future.Get(ctx, &providerResult); err != nil {
				logger.Error("Provider scrape failed", "source", item.source, "err", err)
				providerResult = scraper.Result{
					Status:    "error",
					JobSource: item.source,
					Error:     err.Error(),
				}
				var appErr *temporal.ApplicationError
				if errors.As(err, &appErr) && appErr != nil && appErr.HasDetails() {
					var detail scraper.Result
					if detailErr := appErr.Details(&detail); detailErr == nil && detail.JobSource != "" {
						providerResult = detail
						if providerResult.Status == "" {
							providerResult.Status = "error"
						}
					}
				}
			} else if providerResult.Status == "error" {
				logger.Error("Provider scrape returned error status", "source", item.source, "error", providerResult.Error)
			}
			results = append(results, providerResult)
		}
		result = scraper.CombineResults(results)
	}

	wakeFilterPending(ctx, logger)
	logger.Info("ScrapeWorkflow complete", "result", result)
	return result, nil
}

func wakeFilterPending(ctx workflow.Context, logger log.Logger) {
	wakeCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})
	if err := workflow.ExecuteActivity(wakeCtx, ActivityWakeFilterPending).Get(ctx, nil); err != nil {
		logger.Error("Wake filter-pending failed", "err", err)
	}
}

// FilterPendingWorkflow drains pending rows with cheap checks only. It never
// GETs provider detail HTML.
func FilterPendingWorkflow(ctx workflow.Context) error {
	dbCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})
	wakeCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})
	skipIDs := make([]int64, 0)
	wokeDetail := false

	for processed := 0; processed < filterPendingMaxJobs; processed++ {
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
			return workflow.NewContinueAsNewError(ctx, FilterPendingWorkflow)
		}

		var result FilterPendingJobResult
		if err := workflow.ExecuteActivity(dbCtx, ActivityFilterPendingJob, jobID).Get(ctx, &result); err != nil {
			skipIDs = append(skipIDs, jobID)
			continue
		}
		if result.WroteNeedsDetail && !wokeDetail {
			if err := workflow.ExecuteActivity(wakeCtx, ActivityWakeProcessPending).Get(ctx, nil); err != nil {
				workflow.GetLogger(ctx).Error("Wake process-pending failed", "err", err)
			} else {
				wokeDetail = true
			}
		}
	}

	return workflow.NewContinueAsNewError(ctx, FilterPendingWorkflow)
}

// ProcessPendingWorkflow serially drains needs_detail rows and rolls its
// history after 50 children or one idle wait.
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
			ActivityLoadNextNeedsDetailJob,
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

		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID:               ProcessJobWorkflowID(jobID),
			WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
			WorkflowExecutionTimeout: 30 * time.Minute,
		})
		var result ProcessJobResult
		childErr := workflow.ExecuteChildWorkflow(childCtx, ProcessJobWorkflow, jobID).Get(ctx, &result)
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

// ProcessJobWorkflow fetches a description for one needs_detail row and applies
// description filters. Cheap title/company/duplicate checks run earlier.
func ProcessJobWorkflow(ctx workflow.Context, jobID int64) (ProcessJobResult, error) {
	dbCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})
	getCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	})

	var loaded ProcessJob
	if err := workflow.ExecuteActivity(dbCtx, ActivityLoadProcessJob, jobID).Get(ctx, &loaded); err != nil {
		return ProcessJobResult{}, err
	}
	if loaded.State != domain.StateNeedsDetail {
		return ProcessJobResult{}, nil
	}

	var lists domain.Lists
	if err := workflow.ExecuteActivity(dbCtx, ActivityLoadFilterLists).Get(ctx, &lists); err != nil {
		return ProcessJobResult{}, err
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

	var status NotificationStatus
	if err := workflow.ExecuteActivity(dbCtx, ActivityNotificationStatus).Get(ctx, &status); err != nil {
		return err
	}
	if !status.Active {
		logger.Info("Notifications disabled", "reason", status.Reason)
		return nil
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
