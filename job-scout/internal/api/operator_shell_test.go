package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

func TestServer_UnknownAPIReturnsJSONAndClientRouteReturnsShell(t *testing.T) {
	h := &Handler{}
	server := NewServer("", h)

	apiRequest := httptest.NewRequest(http.MethodGet, "/api/v0/not-a-route", nil)
	apiResponse := httptest.NewRecorder()
	server.Handler.ServeHTTP(apiResponse, apiRequest)
	if apiResponse.Code != http.StatusNotFound {
		t.Errorf("unknown API status = %d, want %d", apiResponse.Code, http.StatusNotFound)
	}
	if contentType := apiResponse.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Errorf("unknown API Content-Type = %q, want application/json", contentType)
	}
	var apiError map[string]string
	if err := json.Unmarshal(apiResponse.Body.Bytes(), &apiError); err != nil {
		t.Fatalf("unknown API JSON: %v", err)
	}
	if apiError["detail"] == "" {
		t.Error("unknown API response must include detail")
	}

	for _, path := range []string{"/", "/dashboard", "/jobs", "/settings", "/does-not-exist"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Errorf("%s status = %d, want %d", path, response.Code, http.StatusOK)
			}
			if !strings.Contains(response.Body.String(), "<html") {
				t.Errorf("%s did not return the shell document: %q", path, response.Body.String())
			}
		})
	}
}

func TestStatus_ReportsDependenciesIndependently(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	temporalErr := errors.New("temporal unavailable")
	h := &Handler{DB: pool, Temporal: &statusTemporalFake{err: temporalErr}}

	request := httptest.NewRequest(http.MethodGet, "/api/v0/status", nil)
	response := httptest.NewRecorder()
	NewServer("", h).Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var body struct {
		OK       bool `json:"ok"`
		Database struct {
			OK bool `json:"ok"`
		} `json:"database"`
		Temporal struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"temporal"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("status JSON: %v", err)
	}
	if body.OK {
		t.Error("overall status = true, want false")
	}
	if !body.Database.OK {
		t.Error("database status = false, want true")
	}
	if body.Temporal.OK {
		t.Error("temporal status = true, want false")
	}
	if !strings.Contains(body.Temporal.Error, temporalErr.Error()) {
		t.Errorf("temporal error = %q, want it to contain %q", body.Temporal.Error, temporalErr)
	}
}

func TestStatus_TemporalCheckHasTwoSecondDeadline(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	temporal := &statusTemporalFake{waitForCancellation: true}
	h := &Handler{DB: pool, Temporal: temporal}

	request := httptest.NewRequest(http.MethodGet, "/api/v0/status", nil)
	response := httptest.NewRecorder()
	NewServer("", h).Handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if temporal.deadlineRemaining <= 0 || temporal.deadlineRemaining > 2*time.Second {
		t.Errorf("Temporal deadline remaining = %s, want > 0 and <= 2s", temporal.deadlineRemaining)
	}
}

func TestConfig_IncludesTemporalUIAddressAndReportingTimezone(t *testing.T) {
	t.Setenv("TEMPORAL_UI_ADDRESS", "http://temporal.example:8080")
	t.Setenv("REPORTING_TIMEZONE", "America/Chicago")
	h := &Handler{Cfg: config.Load()}
	request := httptest.NewRequest(http.MethodGet, "/api/v0/config", nil)
	response := httptest.NewRecorder()
	NewServer("", h).Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("config status = %d, want %d", response.Code, http.StatusOK)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("config JSON: %v", err)
	}
	if body["temporal_ui_address"] != "http://temporal.example:8080" {
		t.Errorf("temporal_ui_address = %v", body["temporal_ui_address"])
	}
	if body["reporting_timezone"] != "America/Chicago" {
		t.Errorf("reporting_timezone = %v", body["reporting_timezone"])
	}
}

func TestHealth_ExistingContractIsUnchanged(t *testing.T) {
	h := &Handler{}
	server := NewServer("", h)
	for _, path := range []string{"/health", "/api/v0/health"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Errorf("%s status = %d, want %d", path, response.Code, http.StatusOK)
		}
		var body map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s JSON: %v", path, err)
		}
		if body["status"] != "healthy" || body["timestamp"] == "" {
			t.Errorf("%s body = %#v, want existing healthy contract", path, body)
		}
	}
}

type statusTemporalFake struct {
	err                 error
	waitForCancellation bool
	deadlineRemaining   time.Duration
}

func (f *statusTemporalFake) ExecuteWorkflow(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) (client.WorkflowRun, error) {
	panic("ExecuteWorkflow unused in status tests")
}

func (f *statusTemporalFake) DescribeWorkflowExecution(context.Context, string, string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	panic("DescribeWorkflowExecution unused in status tests")
}

func (f *statusTemporalFake) CheckHealth(ctx context.Context, _ *client.CheckHealthRequest) (*client.CheckHealthResponse, error) {
	if deadline, ok := ctx.Deadline(); ok {
		f.deadlineRemaining = time.Until(deadline)
	}
	if f.waitForCancellation {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.err != nil {
		return nil, f.err
	}
	return &client.CheckHealthResponse{}, nil
}
