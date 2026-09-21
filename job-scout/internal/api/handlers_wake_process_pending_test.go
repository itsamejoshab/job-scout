package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
	"github.com/jobscout/jobscout/internal/pipeline"
	"github.com/jobscout/jobscout/internal/scraper"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

func TestStartDispatchers_TerminatesLegacyAndStartsBoth(t *testing.T) {
	fake := &wakeTemporalFake{}
	h := &Handler{Temporal: fake}

	if err := h.StartDispatchers(context.Background()); err != nil {
		t.Fatalf("API boot must start the dispatchers: %v", err)
	}
	if len(fake.terminates) != 1 || fake.terminates[0] != pipeline.LegacyProcessPendingWorkflowID {
		t.Errorf("terminates = %v, want [%s]", fake.terminates, pipeline.LegacyProcessPendingWorkflowID)
	}
	assertWakeCalls(t, fake, "filter-pending", "process-pending")
}

func TestStartDispatchers_IgnoresAlreadyStarted(t *testing.T) {
	fake := &wakeTemporalFake{
		err: serviceerror.NewWorkflowExecutionAlreadyStarted(
			"already running", "start-request", "run-id"),
	}
	h := &Handler{Temporal: fake}

	if err := h.StartDispatchers(context.Background()); err != nil {
		t.Errorf("already-started must be ignored on boot, got %v", err)
	}
	if len(fake.calls) != 2 {
		t.Errorf("SignalWithStartWorkflow calls = %d, want 2", len(fake.calls))
	}
}

func TestStartDispatchers_IgnoresMissingLegacy(t *testing.T) {
	fake := &wakeTemporalFake{terminateErr: serviceerror.NewNotFound("missing")}
	h := &Handler{Temporal: fake}

	if err := h.StartDispatchers(context.Background()); err != nil {
		t.Fatalf("missing legacy singleton must not fail boot: %v", err)
	}
	assertWakeCalls(t, fake, "filter-pending", "process-pending")
}

func TestReEvaluateJobs_WakesFilterPendingWhenRowsMoveBackToPending(t *testing.T) {
	pool := newRejectedJobPool(t)
	fake := &wakeTemporalFake{}
	h := &Handler{DB: pool, Temporal: fake}

	if updated := postReEvaluate(t, h); updated != 1 {
		t.Fatalf("updated = %d, want 1 rejected row", updated)
	}
	assertWakeCalls(t, fake, "filter-pending")
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

func TestScrape_SyncScrapeWakesFilterPending(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
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
	assertWakeCalls(t, fake, "filter-pending")
}

func TestScrape_JobSourceDiceUsesDiceProvider(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := &Handler{DB: pool, Scraper: scraper.NewService(pool), Temporal: &wakeTemporalFake{}}
	rec := httptest.NewRecorder()
	NewServer("", h).Handler.ServeHTTP(
		rec, httptest.NewRequest(http.MethodPost, "/api/v0/scrape?job_source=DICE", nil))
	if rec.Code == http.StatusInternalServerError && strings.Contains(rec.Body.String(), "no scraper available") {
		t.Fatalf("POST /api/v0/scrape?job_source=DICE must use the Dice provider, body=%s", rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v0/scrape?job_source=DICE status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body scraper.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode scrape: %v", err)
	}
	if body.JobSource != "DICE" {
		t.Errorf("job_source = %q, want DICE", body.JobSource)
	}
	if !strings.Contains(strings.ToLower(body.Error), "token") {
		t.Errorf("empty token scrape error = %q, want token missing", body.Error)
	}
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

func assertWakeCalls(t *testing.T, fake *wakeTemporalFake, wantIDs ...string) {
	t.Helper()
	if len(fake.calls) != len(wantIDs) {
		t.Fatalf("SignalWithStartWorkflow calls = %d, want %d (%v)", len(fake.calls), len(wantIDs), wantIDs)
	}
	for i, wantID := range wantIDs {
		call := fake.calls[i]
		if call.workflowID != wantID {
			t.Errorf("call %d workflow id = %q, want %q", i, call.workflowID, wantID)
		}
		if call.signalName != "JobsAvailable" {
			t.Errorf("call %d signal name = %q, want JobsAvailable", i, call.signalName)
		}
		if call.signalArg != nil {
			t.Errorf("call %d signal payload = %v, want empty", i, call.signalArg)
		}
		if call.options.TaskQueue != config.TaskQueue {
			t.Errorf("call %d task queue = %q, want %q", i, call.options.TaskQueue, config.TaskQueue)
		}
		wantName := "FilterPendingWorkflow"
		if wantID == "process-pending" {
			wantName = "ProcessPendingWorkflow"
		}
		if name := workflowFuncName(call.workflow); name != wantName {
			t.Errorf("call %d started workflow = %s, want %s", i, name, wantName)
		}
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
	calls        []wakeSignalStart
	terminates   []string
	err          error
	terminateErr error
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

func (f *wakeTemporalFake) TerminateWorkflow(_ context.Context, workflowID string, _ string, _ string, _ ...interface{}) error {
	f.terminates = append(f.terminates, workflowID)
	return f.terminateErr
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
