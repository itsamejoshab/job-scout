package pipeline

import (
	"fmt"
	"log/slog"

	"github.com/jobscout/jobscout/internal/config"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// Registrar is the Temporal registration surface the worker uses.
type Registrar interface {
	RegisterWorkflowWithOptions(workflow interface{}, options workflow.RegisterOptions)
	RegisterActivityWithOptions(activity interface{}, options activity.RegisterOptions)
}

// Register attaches every production workflow and activity. Tests use this.
func Register(r Registrar, acts *Activities) {
	RegisterMain(r, acts)
	RegisterApifyScrapeActivities(r, acts)
	RegisterLinkedInScrapeActivities(r, acts)
}

// RegisterMain attaches parent scrape, filter, process, and notify workflows.
// Provider scrape activities are registered only on their serial queues.
func RegisterMain(r Registrar, acts *Activities) {
	r.RegisterWorkflowWithOptions(ScrapeWorkflow, workflow.RegisterOptions{Name: "ScrapeWorkflow"})
	r.RegisterWorkflowWithOptions(FilterPendingWorkflow, workflow.RegisterOptions{Name: "FilterPendingWorkflow"})
	r.RegisterWorkflowWithOptions(ProcessPendingWorkflow, workflow.RegisterOptions{Name: "ProcessPendingWorkflow"})
	r.RegisterWorkflowWithOptions(ProcessJobWorkflow, workflow.RegisterOptions{Name: "ProcessJobWorkflow"})
	r.RegisterWorkflowWithOptions(NotifyWorkflow, workflow.RegisterOptions{Name: "NotifyWorkflow"})

	r.RegisterActivityWithOptions(acts.list_due_providers, activity.RegisterOptions{Name: ActivityListDueProviders})
	r.RegisterActivityWithOptions(acts.wake_filter_pending, activity.RegisterOptions{Name: ActivityWakeFilterPending})
	r.RegisterActivityWithOptions(acts.wake_process_pending, activity.RegisterOptions{Name: ActivityWakeProcessPending})
	r.RegisterActivityWithOptions(acts.load_next_pending_job, activity.RegisterOptions{Name: ActivityLoadNextPendingJob})
	r.RegisterActivityWithOptions(acts.load_next_needs_detail_job, activity.RegisterOptions{Name: ActivityLoadNextNeedsDetailJob})
	r.RegisterActivityWithOptions(acts.filter_pending_job, activity.RegisterOptions{Name: ActivityFilterPendingJob})
	r.RegisterActivityWithOptions(acts.load_process_job, activity.RegisterOptions{Name: ActivityLoadProcessJob})
	r.RegisterActivityWithOptions(acts.load_filter_lists, activity.RegisterOptions{Name: ActivityLoadFilterLists})
	r.RegisterActivityWithOptions(acts.get_job_description, activity.RegisterOptions{Name: ActivityGetJobDescription})
	r.RegisterActivityWithOptions(acts.save_job_filter_result, activity.RegisterOptions{Name: ActivitySaveJobFilterResult})
	r.RegisterActivityWithOptions(acts.claim_notification_batch, activity.RegisterOptions{Name: ActivityClaimNotificationBatch})
	r.RegisterActivityWithOptions(acts.send_notification, activity.RegisterOptions{Name: ActivitySendNotification})
	r.RegisterActivityWithOptions(acts.finish_notification_batch, activity.RegisterOptions{Name: ActivityFinishNotificationBatch})
	r.RegisterActivityWithOptions(acts.notification_status, activity.RegisterOptions{Name: ActivityNotificationStatus})
}

// RegisterApifyScrapeActivities attaches Dice and Indeed scrape activities.
func RegisterApifyScrapeActivities(r Registrar, acts *Activities) {
	r.RegisterActivityWithOptions(acts.scrape_provider, activity.RegisterOptions{Name: ActivityScrapeProviderDice})
	r.RegisterActivityWithOptions(acts.scrape_provider, activity.RegisterOptions{Name: ActivityScrapeProviderIndeed})
}

// RegisterLinkedInScrapeActivities attaches the LinkedIn scrape activity.
func RegisterLinkedInScrapeActivities(r Registrar, acts *Activities) {
	r.RegisterActivityWithOptions(acts.scrape_provider, activity.RegisterOptions{Name: ActivityScrapeProviderLinkedIn})
}

// serialScrapeWorkerOptions runs one scrape activity at a time on this queue.
func serialScrapeWorkerOptions() worker.Options {
	return worker.Options{
		MaxConcurrentActivityExecutionSize:     1,
		MaxConcurrentWorkflowTaskExecutionSize: 1000,
	}
}

// RunWorker starts the main queue plus serial Apify and LinkedIn activity queues.
func RunWorker(c client.Client, acts *Activities) error {
	mainW := worker.New(c, config.TaskQueue, worker.Options{})
	RegisterMain(mainW, acts)

	apifyW := worker.New(c, config.ApifyScrapeTaskQueue, serialScrapeWorkerOptions())
	RegisterApifyScrapeActivities(apifyW, acts)

	linkedInW := worker.New(c, config.LinkedInScrapeTaskQueue, serialScrapeWorkerOptions())
	RegisterLinkedInScrapeActivities(linkedInW, acts)

	slog.Info("starting temporal workers",
		"main", config.TaskQueue,
		"apify", config.ApifyScrapeTaskQueue,
		"linkedin", config.LinkedInScrapeTaskQueue,
	)

	if err := mainW.Start(); err != nil {
		return fmt.Errorf("start main worker: %w", err)
	}
	defer mainW.Stop()
	if err := apifyW.Start(); err != nil {
		return fmt.Errorf("start apify scrape worker: %w", err)
	}
	defer apifyW.Stop()
	if err := linkedInW.Start(); err != nil {
		return fmt.Errorf("start linkedin scrape worker: %w", err)
	}
	defer linkedInW.Stop()

	<-worker.InterruptCh()
	return nil
}
