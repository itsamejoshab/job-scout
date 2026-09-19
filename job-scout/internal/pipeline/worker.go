package pipeline

import (
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

// Register attaches production workflows and activities to a Temporal worker.
func Register(r Registrar, acts *Activities) {
	r.RegisterWorkflowWithOptions(ScrapeWorkflow, workflow.RegisterOptions{Name: "ScrapeWorkflow"})
	r.RegisterWorkflowWithOptions(NotifyWorkflow, workflow.RegisterOptions{Name: "NotifyWorkflow"})

	r.RegisterActivityWithOptions(acts.scrape_jobs, activity.RegisterOptions{Name: ActivityScrapeJobs})
	r.RegisterActivityWithOptions(acts.load_jobs_for_filtering, activity.RegisterOptions{Name: ActivityLoadJobsForFiltering})
	r.RegisterActivityWithOptions(acts.get_job_description, activity.RegisterOptions{Name: ActivityGetJobDescription})
	r.RegisterActivityWithOptions(acts.save_job_filter_result, activity.RegisterOptions{Name: ActivitySaveJobFilterResult})
	r.RegisterActivityWithOptions(acts.claim_notification_batch, activity.RegisterOptions{Name: ActivityClaimNotificationBatch})
	r.RegisterActivityWithOptions(acts.send_notification, activity.RegisterOptions{Name: ActivitySendNotification})
	r.RegisterActivityWithOptions(acts.finish_notification_batch, activity.RegisterOptions{Name: ActivityFinishNotificationBatch})
}

// RunWorker registers the workflow + activities and blocks until interrupted.
func RunWorker(c client.Client, acts *Activities) error {
	w := worker.New(c, config.TaskQueue, worker.Options{})
	Register(w, acts)
	return w.Run(worker.InterruptCh())
}
