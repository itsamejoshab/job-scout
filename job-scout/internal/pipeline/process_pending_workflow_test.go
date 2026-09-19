package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type pendingQueueProbe struct {
	pending   []int64
	loads     [][]int64
	childSeen []int64
	inChild   bool
	overlap   bool
	completed map[int64]bool
	results   map[int64]ProcessJobResult
	errs      map[int64]error
}

func (p *pendingQueueProbe) load(_ context.Context, input LoadNextPendingInput) (int64, error) {
	skip := input.SkipIDs
	p.loads = append(p.loads, append([]int64(nil), skip...))
	skipped := make(map[int64]bool, len(skip))
	for _, id := range skip {
		skipped[id] = true
	}
	for _, id := range p.pending {
		if !skipped[id] && !p.completed[id] {
			return id, nil
		}
	}
	return 0, nil
}

func (p *pendingQueueProbe) child(_ workflow.Context, id int64) (ProcessJobResult, error) {
	if p.inChild {
		p.overlap = true
	}
	p.inChild = true
	defer func() { p.inChild = false }()
	p.childSeen = append(p.childSeen, id)
	if err := p.errs[id]; err != nil {
		return ProcessJobResult{}, err
	}
	p.completed[id] = true
	return p.results[id], nil
}

func newPendingWorkflowEnv(t *testing.T, probe *pendingQueueProbe) (*testsuite.TestWorkflowEnvironment, *[]time.Duration) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	probe.completed = map[int64]bool{}
	env.RegisterActivityWithOptions(probe.load, activity.RegisterOptions{Name: "load_next_pending_job"})
	env.RegisterWorkflowWithOptions(probe.child, workflow.RegisterOptions{Name: "ProcessJobWorkflow"})
	var timers []time.Duration
	env.SetOnTimerScheduledListener(func(timerID string, duration time.Duration) {
		_ = timerID
		timers = append(timers, duration)
	})
	return env, &timers
}

func TestProcessPendingWorkflow_FIFOSequentialSkipAndThrottle(t *testing.T) {
	probe := &pendingQueueProbe{
		pending: []int64{11, 12, 13},
		results: map[int64]ProcessJobResult{12: {Throttle: true}},
		errs:    map[int64]error{11: errors.New("poison child")},
	}
	env, timers := newPendingWorkflowEnv(t, probe)

	env.ExecuteWorkflow(ProcessPendingWorkflow)

	if !env.IsWorkflowCompleted() {
		t.Fatal("dispatcher must finish its run with continue-as-new")
	}
	if !isContinueAsNewTestError(env.GetWorkflowError()) {
		t.Fatalf("dispatcher error = %v, want continue-as-new", env.GetWorkflowError())
	}
	if probe.overlap {
		t.Error("children overlapped; dispatcher must wait for each child")
	}
	if got := fmt.Sprint(probe.childSeen); got != "[11 12 13]" {
		t.Errorf("child order = %s, want FIFO [11 12 13]", got)
	}
	if len(probe.loads) < 2 || fmt.Sprint(probe.loads[1]) != "[11]" {
		t.Errorf("load skip lists = %v, want failed id 11 excluded after child error", probe.loads)
	}
	assertThrottleTimers(t, *timers, 2)
}

func TestProcessPendingWorkflow_NoThrottleSleepsForOrdinaryChildren(t *testing.T) {
	probe := &pendingQueueProbe{
		pending: []int64{21},
		results: map[int64]ProcessJobResult{},
		errs:    map[int64]error{},
	}
	env, timers := newPendingWorkflowEnv(t, probe)

	env.ExecuteWorkflow(ProcessPendingWorkflow)

	if !isContinueAsNewTestError(env.GetWorkflowError()) {
		t.Fatalf("dispatcher error = %v, want continue-as-new", env.GetWorkflowError())
	}
	assertThrottleTimers(t, *timers, 0)
}

func TestProcessPendingWorkflow_ContinuesAsNewAfterFiftyChildren(t *testing.T) {
	ids := make([]int64, 50)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	probe := &pendingQueueProbe{pending: ids, results: map[int64]ProcessJobResult{}, errs: map[int64]error{}}
	env, _ := newPendingWorkflowEnv(t, probe)

	env.ExecuteWorkflow(ProcessPendingWorkflow)

	if !isContinueAsNewTestError(env.GetWorkflowError()) {
		t.Fatalf("dispatcher error = %v, want continue-as-new", env.GetWorkflowError())
	}
	if len(probe.childSeen) != 50 {
		t.Errorf("children started = %d, want 50", len(probe.childSeen))
	}
}

func TestProcessPendingWorkflow_IdleSignalWakesThenContinuesAsNew(t *testing.T) {
	probe := &pendingQueueProbe{results: map[int64]ProcessJobResult{}, errs: map[int64]error{}}
	env, timers := newPendingWorkflowEnv(t, probe)
	startedAt := env.Now()
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("JobsAvailable", nil)
	}, 30*time.Second)

	env.ExecuteWorkflow(ProcessPendingWorkflow)

	if !isContinueAsNewTestError(env.GetWorkflowError()) {
		t.Fatalf("dispatcher error = %v, want continue-as-new", env.GetWorkflowError())
	}
	if !containsDuration(*timers, 2*time.Minute) {
		t.Errorf("timers = %v, want 2m idle timer", *timers)
	}
	if elapsed := env.Now().Sub(startedAt); elapsed != 30*time.Second {
		t.Errorf("signal idle wait elapsed = %s, want 30s", elapsed)
	}
}

func TestProcessPendingWorkflow_IdleTimeoutContinuesAsNew(t *testing.T) {
	probe := &pendingQueueProbe{results: map[int64]ProcessJobResult{}, errs: map[int64]error{}}
	env, timers := newPendingWorkflowEnv(t, probe)
	startedAt := env.Now()

	env.ExecuteWorkflow(ProcessPendingWorkflow)

	if !isContinueAsNewTestError(env.GetWorkflowError()) {
		t.Fatalf("dispatcher error = %v, want continue-as-new", env.GetWorkflowError())
	}
	if !containsDuration(*timers, 2*time.Minute) {
		t.Errorf("timers = %v, want 2m idle timer", *timers)
	}
	if elapsed := env.Now().Sub(startedAt); elapsed != 2*time.Minute {
		t.Errorf("idle timeout elapsed = %s, want 2m", elapsed)
	}
}

func TestProcessPendingWorkflow_UsesReservedSingletonID(t *testing.T) {
	if ProcessPendingWorkflowID != "jobscout-process-pending" {
		t.Errorf("dispatcher workflow ID = %q, want jobscout-process-pending", ProcessPendingWorkflowID)
	}
}

func assertThrottleTimers(t *testing.T, timers []time.Duration, want int) {
	t.Helper()
	got := 0
	for _, d := range timers {
		if d >= time.Minute && d <= 90*time.Second {
			if d%time.Second != 0 {
				t.Errorf("throttle timer %s must use whole-second jitter", d)
			}
			got++
			continue
		}
		if d != 2*time.Minute {
			t.Errorf("unexpected timer duration %s", d)
		}
	}
	if got != want {
		t.Errorf("throttle timers = %d (%v), want %d", got, timers, want)
	}
}

func containsDuration(ds []time.Duration, want time.Duration) bool {
	for _, d := range ds {
		if d == want {
			return true
		}
	}
	return false
}

func isContinueAsNewTestError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "continue as new")
}
