package pipeline

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

type filterQueueProbe struct {
	pending     []int64
	completed   map[int64]bool
	filterSeen  []int64
	results     map[int64]FilterPendingJobResult
	errs        map[int64]error
	wakeProcess int
	detailGets  int
}

func (p *filterQueueProbe) load(_ context.Context, input LoadNextPendingInput) (int64, error) {
	skipped := make(map[int64]bool, len(input.SkipIDs))
	for _, id := range input.SkipIDs {
		skipped[id] = true
	}
	for _, id := range p.pending {
		if !skipped[id] && !p.completed[id] {
			return id, nil
		}
	}
	return 0, nil
}

func (p *filterQueueProbe) filter(_ context.Context, jobID int64) (FilterPendingJobResult, error) {
	p.filterSeen = append(p.filterSeen, jobID)
	if err := p.errs[jobID]; err != nil {
		return FilterPendingJobResult{}, err
	}
	p.completed[jobID] = true
	return p.results[jobID], nil
}

func (p *filterQueueProbe) wake(_ context.Context) error {
	p.wakeProcess++
	return nil
}

func (p *filterQueueProbe) getDetail(_ context.Context, _ DetailFetchInput) (DetailFetchResult, error) {
	p.detailGets++
	return DetailFetchResult{}, nil
}

func newFilterWorkflowEnv(t *testing.T, probe *filterQueueProbe) (*testsuite.TestWorkflowEnvironment, *[]time.Duration) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	probe.completed = map[int64]bool{}
	env.RegisterActivityWithOptions(probe.load, activity.RegisterOptions{Name: ActivityLoadNextPendingJob})
	env.RegisterActivityWithOptions(probe.filter, activity.RegisterOptions{Name: ActivityFilterPendingJob})
	env.RegisterActivityWithOptions(probe.wake, activity.RegisterOptions{Name: ActivityWakeProcessPending})
	env.RegisterActivityWithOptions(probe.getDetail, activity.RegisterOptions{Name: ActivityGetJobDescription})
	var timers []time.Duration
	env.SetOnTimerScheduledListener(func(timerID string, duration time.Duration) {
		_ = timerID
		timers = append(timers, duration)
	})
	return env, &timers
}

func TestFilterPendingWorkflow_DrainsWithoutDetailGET(t *testing.T) {
	probe := &filterQueueProbe{
		pending: []int64{1, 2},
		results: map[int64]FilterPendingJobResult{
			1: {},
			2: {WroteNeedsDetail: true},
		},
		errs: map[int64]error{},
	}
	env, _ := newFilterWorkflowEnv(t, probe)

	env.ExecuteWorkflow(FilterPendingWorkflow)

	if !isContinueAsNewTestError(env.GetWorkflowError()) {
		t.Fatalf("filter dispatcher error = %v, want continue-as-new", env.GetWorkflowError())
	}
	if probe.detailGets != 0 {
		t.Errorf("fast filter must never GET detail HTML, gets=%d", probe.detailGets)
	}
	if got := fmt.Sprint(probe.filterSeen); got != "[1 2]" {
		t.Errorf("filtered order = %s, want [1 2]", got)
	}
	if probe.wakeProcess != 1 {
		t.Errorf("wake process-pending calls = %d, want 1 after first needs_detail", probe.wakeProcess)
	}
}

func TestFilterPendingWorkflow_WakesProcessPendingOncePerRun(t *testing.T) {
	probe := &filterQueueProbe{
		pending: []int64{5, 6, 7},
		results: map[int64]FilterPendingJobResult{
			5: {WroteNeedsDetail: true},
			6: {WroteNeedsDetail: true},
			7: {WroteNeedsDetail: true},
		},
		errs: map[int64]error{},
	}
	env, _ := newFilterWorkflowEnv(t, probe)

	env.ExecuteWorkflow(FilterPendingWorkflow)

	if !isContinueAsNewTestError(env.GetWorkflowError()) {
		t.Fatalf("filter dispatcher error = %v, want continue-as-new", env.GetWorkflowError())
	}
	if probe.wakeProcess != 1 {
		t.Errorf("wake process-pending calls = %d, want 1 per continue-as-new run", probe.wakeProcess)
	}
}

func TestFilterPendingWorkflow_IdleTimeoutContinuesAsNew(t *testing.T) {
	probe := &filterQueueProbe{results: map[int64]FilterPendingJobResult{}, errs: map[int64]error{}}
	env, timers := newFilterWorkflowEnv(t, probe)
	startedAt := env.Now()

	env.ExecuteWorkflow(FilterPendingWorkflow)

	if !isContinueAsNewTestError(env.GetWorkflowError()) {
		t.Fatalf("filter dispatcher error = %v, want continue-as-new", env.GetWorkflowError())
	}
	if !containsDuration(*timers, time.Hour) {
		t.Errorf("timers = %v, want 1h idle timer", *timers)
	}
	if elapsed := env.Now().Sub(startedAt); elapsed != time.Hour {
		t.Errorf("idle timeout elapsed = %s, want 1h", elapsed)
	}
}
