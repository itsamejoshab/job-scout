package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/scraper"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

// wakeActivityName pins the registered name of the wake activity.
const wakeActivityName = "wake_process_pending"

func (p *activityProbe) WakeProcessPending(context.Context) error {
	p.wake++
	return p.wakeErr
}

func TestScrapeWorkflow_WakesProcessPendingAfterScrapeSucceeds(t *testing.T) {
	env, started, probe := newWorkflowEnv()

	env.ExecuteWorkflow(ScrapeWorkflow, scraper.TickInput{JobSource: "LINKEDIN"})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ScrapeWorkflow error: %v", err)
	}
	if probe.wake != 1 {
		t.Errorf("wake activity calls = %d, want 1 after a successful scrape", probe.wake)
	}
	assertActivityNames(t, *started, ActivityScrapeJobs, wakeActivityName)
}

func TestScrapeWorkflow_WakesProcessPendingWhenScrapeFails(t *testing.T) {
	env, started, probe := newWorkflowEnv()
	probe.scrapeErr = errors.New("linkedin search failed")

	env.ExecuteWorkflow(ScrapeWorkflow, scraper.TickInput{JobSource: "LINKEDIN"})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ScrapeWorkflow must stay green after a failed scrape: %v", err)
	}
	var got scraper.Result
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatalf("read ScrapeWorkflow result: %v", err)
	}
	if got.Status != "error" {
		t.Errorf("result status = %q, want error", got.Status)
	}
	if probe.wake != 1 {
		t.Errorf("wake activity calls = %d, want 1; pending rows may already exist", probe.wake)
	}
	assertActivityNames(t, *started, ActivityScrapeJobs, wakeActivityName)
}

func TestScrapeWorkflow_WakesProcessPendingWhenScrapeSkipped(t *testing.T) {
	env, started, probe := newWorkflowEnv()
	probe.result = scraper.Result{Status: "skipped"}

	env.ExecuteWorkflow(ScrapeWorkflow, scraper.TickInput{})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ScrapeWorkflow error: %v", err)
	}
	if probe.wake != 1 {
		t.Errorf("wake activity calls = %d, want 1 on a skipped scrape", probe.wake)
	}
	assertActivityNames(t, *started, ActivityScrapeJobs, wakeActivityName)
}

func TestScrapeWorkflow_FailedWakeKeepsScrapeResult(t *testing.T) {
	env, _, probe := newWorkflowEnv()
	probe.wakeErr = errors.New("temporal unavailable")

	env.ExecuteWorkflow(ScrapeWorkflow, scraper.TickInput{JobSource: "LINKEDIN"})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("a failed wake must not fail ScrapeWorkflow: %v", err)
	}
	var got scraper.Result
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatalf("read ScrapeWorkflow result: %v", err)
	}
	if got.Status != "ok" {
		t.Errorf("result status = %q, want the scrape result ok", got.Status)
	}
}

func TestRegister_RegistersWakeProcessPendingActivity(t *testing.T) {
	rec := &registryRecorder{}
	Register(rec, &Activities{})

	if !rec.hasActivity(wakeActivityName) {
		t.Errorf("worker must register %s; registered %v", wakeActivityName, rec.activities)
	}
}

func TestWakeProcessPending_SignalWithStartsTheSingleton(t *testing.T) {
	starter := &signalStarterFake{}

	if err := WakeProcessPending(context.Background(), starter); err != nil {
		t.Fatalf("WakeProcessPending error: %v", err)
	}

	if len(starter.calls) != 1 {
		t.Fatalf("SignalWithStartWorkflow calls = %d, want 1", len(starter.calls))
	}
	call := starter.calls[0]
	if call.workflowID != "jobscout-process-pending" {
		t.Errorf("workflow id = %q, want jobscout-process-pending", call.workflowID)
	}
	if call.signalName != "JobsAvailable" {
		t.Errorf("signal name = %q, want JobsAvailable", call.signalName)
	}
	if call.signalArg != nil {
		t.Errorf("signal payload = %v, want empty", call.signalArg)
	}
	if call.options.TaskQueue != config.TaskQueue {
		t.Errorf("task queue = %q, want %q", call.options.TaskQueue, config.TaskQueue)
	}
	if name := funcBaseName(call.workflow); name != "ProcessPendingWorkflow" {
		t.Errorf("started workflow = %s, want ProcessPendingWorkflow", name)
	}
	if len(call.workflowArgs) != 0 {
		t.Errorf("workflow args = %v, want none", call.workflowArgs)
	}
}

func TestWakeProcessPending_AlreadyStartedIsNotAnError(t *testing.T) {
	starter := &signalStarterFake{
		err: serviceerror.NewWorkflowExecutionAlreadyStarted(
			"already running", "start-request", "run-id"),
	}

	if err := WakeProcessPending(context.Background(), starter); err != nil {
		t.Errorf("already-started must be ignored, got %v", err)
	}
	if len(starter.calls) != 1 {
		t.Errorf("SignalWithStartWorkflow calls = %d, want 1", len(starter.calls))
	}
}

func TestWakeProcessPending_ReportsOtherErrors(t *testing.T) {
	starter := &signalStarterFake{err: errors.New("temporal unavailable")}

	if err := WakeProcessPending(context.Background(), starter); err == nil {
		t.Error("WakeProcessPending must report a transport error")
	}
}

type signalWithStartCall struct {
	workflowID   string
	signalName   string
	signalArg    any
	options      client.StartWorkflowOptions
	workflow     any
	workflowArgs []any
}

type signalStarterFake struct {
	calls []signalWithStartCall
	err   error
}

func (f *signalStarterFake) SignalWithStartWorkflow(
	_ context.Context,
	workflowID string,
	signalName string,
	signalArg interface{},
	options client.StartWorkflowOptions,
	workflow interface{},
	workflowArgs ...interface{},
) (client.WorkflowRun, error) {
	f.calls = append(f.calls, signalWithStartCall{
		workflowID: workflowID, signalName: signalName, signalArg: signalArg,
		options: options, workflow: workflow,
		workflowArgs: append([]any{}, workflowArgs...),
	})
	if f.err != nil {
		return nil, f.err
	}
	return nil, nil
}
