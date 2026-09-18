package pipeline

import (
	"github.com/jobscout/jobscout/internal/config"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

// Registrar is the Temporal registration surface the worker uses.
type Registrar interface {
	RegisterWorkflow(workflow interface{})
	RegisterActivity(activity interface{})
}

// Register attaches production workflows and activities to a Temporal worker.
func Register(r Registrar, acts *Activities) {
	r.RegisterWorkflow(ScrapeTick)
	r.RegisterWorkflow(NotifyTick)
	r.RegisterActivity(acts)
}

// RunWorker registers the workflow + activities and blocks until interrupted.
func RunWorker(c client.Client, acts *Activities) error {
	w := worker.New(c, config.TaskQueue, worker.Options{})
	Register(w, acts)
	return w.Run(worker.InterruptCh())
}
