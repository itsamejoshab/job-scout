package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNotify_StartsNotifyTickAsyncWithManualWorkflowID(t *testing.T) {
	fake := &runTemporalFake{}
	h := &Handler{Temporal: fake}
	srv := NewServer("", h)

	before := time.Now().UTC().Add(-2 * time.Second)
	id1 := postNotify(t, srv)
	afterFirst := time.Now().UTC().Add(2 * time.Second)
	id2 := postNotify(t, srv)
	afterSecond := time.Now().UTC().Add(2 * time.Second)

	if len(fake.starts) != 2 {
		t.Fatalf("POST /api/v0/notify must start exactly one workflow per request, starts=%d", len(fake.starts))
	}
	assertManualNotifyStart(t, fake.starts[0], id1, before, afterFirst)
	assertManualNotifyStart(t, fake.starts[1], id2, before, afterSecond)
	if id1 == id2 {
		t.Errorf("two manual notify runs must not share a workflow ID, both %q", id1)
	}
	if fake.waited {
		t.Fatal("POST /api/v0/notify must not wait for the workflow result")
	}
}

func postNotify(t *testing.T, srv *http.Server) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v0/notify", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v0/notify status=%d body=%s", rec.Code, rec.Body.String())
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

func assertManualNotifyStart(t *testing.T, start workflowStart, responseID string, before, after time.Time) {
	t.Helper()
	if responseID != start.opts.ID {
		t.Errorf("workflow_id %q must match started workflow ID %q", responseID, start.opts.ID)
	}
	if responseID == "notify-scheduled" {
		t.Errorf("manual notify workflow ID must differ from reserved scheduled ID %q", responseID)
	}
	const prefix = "notify-manual-"
	if !strings.HasPrefix(responseID, prefix) {
		t.Errorf("manual notify workflow ID must start with %q, got %q", prefix, responseID)
		return
	}
	suffix := strings.TrimPrefix(responseID, prefix)
	ts, err := time.Parse(time.RFC3339Nano, suffix)
	if err != nil {
		t.Errorf("manual notify workflow ID suffix must be utc rfc3339 nano, got %q: %v", suffix, err)
		return
	}
	if ts.Location() != time.UTC && suffix[len(suffix)-1] != 'Z' {
		t.Errorf("manual notify workflow ID timestamp must be UTC, got %q", suffix)
	}
	if ts.Before(before) || ts.After(after) {
		t.Errorf("manual notify workflow ID timestamp %s is outside request window [%s, %s]", ts, before, after)
	}
	if name := workflowFuncName(start.workflow); name != "NotifyWorkflow" {
		t.Errorf("POST /api/v0/notify must start NotifyWorkflow asynchronously, got %s (%T)", name, start.workflow)
	}
}
