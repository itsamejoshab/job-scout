package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
	"github.com/jobscout/jobscout/internal/scraper"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

func TestStartProcessPending_BootSignalWithStartsTheSingleton(t *testing.T) {
	fake := &wakeTemporalFake{}
	h := &Handler{Temporal: fake}

	if err := h.StartProcessPending(context.Background()); err != nil {
		t.Fatalf("API boot must start the dispatcher: %v", err)
	}
	assertWakeCall(t, fake)
}

func TestStartProcessPending_IgnoresAlreadyStarted(t *testing.T) {
	fake := &wakeTemporalFake{
		err: serviceerror.NewWorkflowExecutionAlreadyStarted(
			"already running", "start-request", "run-id"),
	}
	h := &Handler{Temporal: fake}

	if err := h.StartProcessPending(context.Background()); err != nil {
		t.Errorf("already-started must be ignored on boot, got %v", err)
	}
	if len(fake.calls) != 1 {
		t.Errorf("SignalWithStartWorkflow calls = %d, want 1", len(fake.calls))
	}
}

func TestReEvaluateJobs_WakesProcessPendingWhenRowsMoveBackToPending(t *testing.T) {
	pool := newRejectedJobPool(t)
	fake := &wakeTemporalFake{}
	h := &Handler{DB: pool, Temporal: fake}

	if updated := postReEvaluate(t, h); updated != 1 {
		t.Fatalf("updated = %d, want 1 rejected row", updated)
	}
	assertWakeCall(t, fake)
}

func TestReEvaluateJobs_NoRowsUpdatedDoesNotWake(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	fake := &wakeTemporalFake{}
	h := &Handler{DB: pool, Temporal: fake}

	if updated := postReEvaluate(t, h); updated != 0 {
		t.Fatalf("updated = %d, want 0", updated)
	}
	if len(fake.calls) != 0 {
		t.Errorf("no rejected rows must not wake the dispatcher, calls = %d", len(fake.calls))
	}
}

func TestScrape_SyncScrapeWakesProcessPending(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	// A disabled provider keeps the debug scrape off the network while it still
	// returns a non-error result.
	if _, err := pool.Exec(`UPDATE scraper_settings SET enabled = FALSE`); err != nil {
		t.Fatalf("disable providers: %v", err)
	}
	fake := &wakeTemporalFake{}
	h := &Handler{DB: pool, Temporal: fake, Scraper: scraper.NewService(pool)}

	rec := httptest.NewRecorder()
	NewServer("", h).Handler.ServeHTTP(
		rec, httptest.NewRequest(http.MethodPost, "/api/v0/scrape?job_source=linkedin", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v0/scrape status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertWakeCall(t, fake)
}

func newRejectedJobPool(t *testing.T) *sql.DB {
	t.Helper()
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.InsertJobIfNew(t.Context(), pool, db.Job{
		JobSource: db.SourceLinkedIn, Title: "Detail Failed", Company: "Acme",
		Location: "Remote", JobURL: "https://example.test/jobs/wake-detail-failed",
	}); err != nil {
		t.Fatalf("insert job: %v", err)
	}
	if _, err := pool.Exec(`
		UPDATE jobs SET state = 'rejected', reject_reason = 'detail_failed', detail_attempts = 3`,
	); err != nil {
		t.Fatalf("reject job: %v", err)
	}
	return pool
}

func postReEvaluate(t *testing.T, h *Handler) int {
	t.Helper()
	rec := httptest.NewRecorder()
	NewServer("", h).Handler.ServeHTTP(
		rec, httptest.NewRequest(http.MethodPost, "/api/v0/jobs/re-evaluate", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v0/jobs/re-evaluate status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Updated *int `json:"updated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("re-evaluate JSON: %v", err)
	}
	if body.Updated == nil {
		t.Fatalf("re-evaluate response must include updated, got %s", rec.Body.String())
	}
	return *body.Updated
}

func assertWakeCall(t *testing.T, fake *wakeTemporalFake) {
	t.Helper()
	if len(fake.calls) != 1 {
		t.Fatalf("SignalWithStartWorkflow calls = %d, want 1", len(fake.calls))
	}
	call := fake.calls[0]
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
	if name := workflowFuncName(call.workflow); name != "ProcessPendingWorkflow" {
		t.Errorf("started workflow = %s, want ProcessPendingWorkflow", name)
	}
}

type wakeSignalStart struct {
	workflowID string
	signalName string
	signalArg  any
	options    client.StartWorkflowOptions
	workflow   any
	args       []any
}

type wakeTemporalFake struct {
	calls []wakeSignalStart
	err   error
}

func (f *wakeTemporalFake) SignalWithStartWorkflow(
	_ context.Context,
	workflowID string,
	signalName string,
	signalArg interface{},
	options client.StartWorkflowOptions,
	workflow interface{},
	workflowArgs ...interface{},
) (client.WorkflowRun, error) {
	f.calls = append(f.calls, wakeSignalStart{
		workflowID: workflowID, signalName: signalName, signalArg: signalArg,
		options: options, workflow: workflow, args: append([]any{}, workflowArgs...),
	})
	if f.err != nil {
		return nil, f.err
	}
	return &runWorkflowFake{id: workflowID, parent: &runTemporalFake{}}, nil
}

func (f *wakeTemporalFake) ExecuteWorkflow(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) (client.WorkflowRun, error) {
	panic("ExecuteWorkflow unused in wake tests")
}

func (f *wakeTemporalFake) DescribeWorkflowExecution(context.Context, string, string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	panic("DescribeWorkflowExecution unused in wake tests")
}

func (f *wakeTemporalFake) CheckHealth(context.Context, *client.CheckHealthRequest) (*client.CheckHealthResponse, error) {
	panic("CheckHealth unused in wake tests")
}
