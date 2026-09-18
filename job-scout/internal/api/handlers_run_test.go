package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

func TestRun_ForceQueryEchoesAndStartsScrapeTick(t *testing.T) {
	fake := &runTemporalFake{}
	h := &Handler{Temporal: fake}
	srv := NewServer("", h)

	for _, raw := range []string{"1", "true"} {
		fake.starts = nil
		req := httptest.NewRequest(http.MethodPost, "/api/v0/run?force="+raw, nil)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/v0/run?force=%s status=%d body=%s", raw, rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("response must be JSON: %v", err)
		}
		if body["force"] != true {
			t.Errorf("POST /api/v0/run?force=%s must echo force=true, body=%v", raw, body)
		}
		if body["workflow_id"] == "" {
			t.Errorf("POST /api/v0/run?force=%s must include workflow_id, body=%v", raw, body)
		}
		if len(fake.starts) != 1 {
			t.Fatalf("POST /api/v0/run?force=%s must start one workflow, starts=%d", raw, len(fake.starts))
		}
		start := fake.starts[0]
		if name := workflowFuncName(start.workflow); name != "ScrapeTick" {
			t.Errorf("force scrape must start ScrapeTick, got %s", name)
		}
		if len(start.args) != 1 {
			t.Fatalf("ScrapeTick args=%v, want one Tick input", start.args)
		}
		assertTickArg(t, start.args, true)
	}
}

func TestRun_ForceDoesNotTargetIndeed(t *testing.T) {
	fake := &runTemporalFake{}
	h := &Handler{Temporal: fake}
	srv := NewServer("", h)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/run?force=1&job_source=INDEED", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v0/run?force=1&job_source=INDEED status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.starts) != 1 {
		t.Fatalf("starts=%d, want 1", len(fake.starts))
	}
	if bodyForce(t, rec) != true {
		t.Errorf("force=1 must echo force=true even when job_source=INDEED")
	}
	assertTickArg(t, fake.starts[0].args, true)
	if jobSourceArg(fake.starts[0].args) == "INDEED" {
		t.Error("force=1 must not select Indeed; ScrapeTick discovers enabled due-or-forced providers")
	}
}

func bodyForce(t *testing.T, rec *httptest.ResponseRecorder) any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response must be JSON: %v", err)
	}
	return body["force"]
}

func assertTickArg(t *testing.T, args []any, wantForce bool) {
	t.Helper()
	if len(args) != 1 {
		t.Errorf("ScrapeTick args=%v, want one TickInput", args)
		return
	}
	if forceArg(args[0]) != wantForce {
		t.Errorf("ScrapeTick force = %v, want %v, args=%v", forceArg(args[0]), wantForce, args)
	}
	if jobSourceArg(args) == "INDEED" {
		t.Error("ScrapeTick must not target Indeed")
	}
}

func forceArg(arg any) bool {
	switch v := arg.(type) {
	case bool:
		return v
	case map[string]any:
		b, _ := v["force"].(bool)
		return b
	default:
		rv := reflect.ValueOf(arg)
		if rv.Kind() == reflect.Struct {
			f := rv.FieldByName("Force")
			if f.IsValid() && f.Kind() == reflect.Bool {
				return f.Bool()
			}
		}
		return false
	}
}

func jobSourceArg(args []any) string {
	if len(args) == 0 {
		return ""
	}
	switch v := args[0].(type) {
	case string:
		return v
	default:
		rv := reflect.ValueOf(v)
		if rv.Kind() == reflect.Struct {
			f := rv.FieldByName("JobSource")
			if f.IsValid() && f.Kind() == reflect.String {
				return f.String()
			}
		}
		return ""
	}
}

func TestRun_StartsScrapeTickAsyncWithManualWorkflowID(t *testing.T) {
	fake := &runTemporalFake{}
	h := &Handler{Temporal: fake}
	srv := NewServer("", h)

	before := time.Now().UTC().Add(-2 * time.Second)
	id1 := postRun(t, srv)
	afterFirst := time.Now().UTC().Add(2 * time.Second)
	id2 := postRun(t, srv)
	afterSecond := time.Now().UTC().Add(2 * time.Second)

	if len(fake.starts) != 2 {
		t.Fatalf("POST /api/v0/run must start exactly one workflow per request, starts=%d", len(fake.starts))
	}
	assertManualScrapeStart(t, fake.starts[0], id1, before, afterFirst)
	assertManualScrapeStart(t, fake.starts[1], id2, before, afterSecond)
	if id1 == id2 {
		t.Errorf("two manual scrape runs must not share a workflow ID, both %q", id1)
	}
	if fake.waited {
		t.Fatal("POST /api/v0/run must not wait for the workflow result")
	}
}

func postRun(t *testing.T, srv *http.Server) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v0/run", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v0/run status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response must be JSON: %v", err)
	}
	gotID, _ := body["workflow_id"].(string)
	if gotID == "" {
		t.Fatalf("response must include workflow_id, got %v", body)
	}
	return gotID
}

func assertManualScrapeStart(t *testing.T, start workflowStart, responseID string, before, after time.Time) {
	t.Helper()
	if responseID != start.opts.ID {
		t.Errorf("workflow_id %q must match started workflow ID %q", responseID, start.opts.ID)
	}
	if responseID == "jobscout-scrape-scheduled" {
		t.Errorf("manual scrape workflow ID must differ from reserved scheduled ID %q", responseID)
	}
	const prefix = "jobscout-scrape-manual-"
	if !strings.HasPrefix(responseID, prefix) {
		t.Errorf("manual scrape workflow ID must start with %q, got %q", prefix, responseID)
		return
	}
	suffix := strings.TrimPrefix(responseID, prefix)
	ts, err := time.Parse(time.RFC3339Nano, suffix)
	if err != nil {
		t.Errorf("manual scrape workflow ID suffix must be utc rfc3339 nano, got %q: %v", suffix, err)
		return
	}
	if ts.Location() != time.UTC && suffix[len(suffix)-1] != 'Z' {
		t.Errorf("manual scrape workflow ID timestamp must be UTC, got %q", suffix)
	}
	if ts.Before(before) || ts.After(after) {
		t.Errorf("manual scrape workflow ID timestamp %s is outside request window [%s, %s]", ts, before, after)
	}
	if name := workflowFuncName(start.workflow); name != "ScrapeTick" {
		t.Errorf("POST /api/v0/run must start ScrapeTick asynchronously, got %s (%T)", name, start.workflow)
	}
	assertTickArg(t, start.args, false)
}

type workflowStart struct {
	opts     client.StartWorkflowOptions
	workflow any
	args     []any
}

type runTemporalFake struct {
	starts []workflowStart
	waited bool
}

func (f *runTemporalFake) ExecuteWorkflow(_ context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error) {
	start := workflowStart{opts: options, workflow: workflow, args: append([]any{}, args...)}
	f.starts = append(f.starts, start)
	return &runWorkflowFake{id: options.ID, parent: f}, nil
}

func (f *runTemporalFake) DescribeWorkflowExecution(context.Context, string, string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	panic("DescribeWorkflowExecution unused in POST /api/v0/run")
}

func (f *runTemporalFake) CheckHealth(context.Context, *client.CheckHealthRequest) (*client.CheckHealthResponse, error) {
	panic("CheckHealth unused in POST /api/v0/run")
}

type runWorkflowFake struct {
	id     string
	parent *runTemporalFake
}

func (r *runWorkflowFake) GetID() string { return r.id }

func (r *runWorkflowFake) GetRunID() string { return "test-run" }

func (r *runWorkflowFake) Get(context.Context, interface{}) error {
	r.parent.waited = true
	return nil
}

func (r *runWorkflowFake) GetWithOptions(context.Context, interface{}, client.WorkflowRunGetOptions) error {
	r.parent.waited = true
	return nil
}

func workflowFuncName(fn any) string {
	if s, ok := fn.(string); ok {
		return s
	}
	v := reflect.ValueOf(fn)
	if !v.IsValid() || v.Kind() != reflect.Func {
		return ""
	}
	name := runtime.FuncForPC(v.Pointer()).Name()
	name = strings.TrimSuffix(name, "-fm")
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return name
}
