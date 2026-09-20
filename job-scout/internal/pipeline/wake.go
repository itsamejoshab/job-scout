package pipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/jobscout/jobscout/internal/config"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

// SignalStarter is the Temporal client surface the wake helper needs.
type SignalStarter interface {
	SignalWithStartWorkflow(ctx context.Context, workflowID string, signalName string, signalArg interface{},
		options client.StartWorkflowOptions, workflow interface{}, workflowArgs ...interface{}) (client.WorkflowRun, error)
}

// WakeFilterPending signals the fast-filter singleton, starting it first when
// it is not running. The signal carries no payload because the dispatcher
// always reloads pending rows from the database.
func WakeFilterPending(ctx context.Context, c SignalStarter) error {
	if c == nil {
		return fmt.Errorf("temporal client is required to wake %s", FilterPendingWorkflowID)
	}
	_, err := c.SignalWithStartWorkflow(
		ctx,
		FilterPendingWorkflowID,
		SignalJobsAvailable,
		nil,
		client.StartWorkflowOptions{
			ID:        FilterPendingWorkflowID,
			TaskQueue: config.TaskQueue,
		},
		FilterPendingWorkflow,
	)
	var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &alreadyStarted) {
		return nil
	}
	return err
}

// WakeProcessPending signals the detail dispatcher singleton, starting it
// first when it is not running. A dispatcher that is already running is the
// wanted state, not an error.
func WakeProcessPending(ctx context.Context, c SignalStarter) error {
	if c == nil {
		return fmt.Errorf("temporal client is required to wake %s", ProcessPendingWorkflowID)
	}
	_, err := c.SignalWithStartWorkflow(
		ctx,
		ProcessPendingWorkflowID,
		SignalJobsAvailable,
		nil,
		client.StartWorkflowOptions{
			ID:        ProcessPendingWorkflowID,
			TaskQueue: config.TaskQueue,
		},
		ProcessPendingWorkflow,
	)
	var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &alreadyStarted) {
		return nil
	}
	return err
}
