package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/pipeline"
	"github.com/jobscout/jobscout/internal/scraper"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

// temporalAPI is the subset of client.Client used by HTTP handlers.
type temporalAPI interface {
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error)
	DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error)
	CheckHealth(ctx context.Context, request *client.CheckHealthRequest) (*client.CheckHealthResponse, error)
}

// Handler carries the dependencies shared by all HTTP handlers.
type Handler struct {
	DB        *sql.DB
	Temporal  temporalAPI
	Schedules pipeline.ScheduleStore
	Scraper   *scraper.Service
	Cfg       config.Config
}

// InstallSchedules creates or updates scrape and notify interval schedules.
func (h *Handler) InstallSchedules(ctx context.Context) error {
	return pipeline.EnsureSchedules(ctx, h.Schedules, h.Cfg)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode response failed", "err", err)
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"detail": msg})
}

func now() string { return time.Now().Format(time.RFC3339) }

// POST /api/v0/run -> start the Temporal workflow.
func (h *Handler) Run(w http.ResponseWriter, r *http.Request) {
	in := scraper.TickInput{Force: parseForce(r)}
	raw := r.URL.Query().Get("job_source")
	if raw != "" {
		source, err := db.ParseJobSource(raw)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if source != db.SourceIndeed {
			in.JobSource = string(source)
		}
	}

	opts := client.StartWorkflowOptions{
		ID:        pipeline.ManualScrapeWorkflowID(time.Now()),
		TaskQueue: config.TaskQueue,
	}
	we, err := h.Temporal.ExecuteWorkflow(r.Context(), opts, pipeline.ScrapeTick, in)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "workflow_started",
		"providers":   "due",
		"force":       in.Force,
		"workflow_id": we.GetID(),
	})
}

// POST /api/v0/notify -> start NotifyTick asynchronously.
func (h *Handler) Notify(w http.ResponseWriter, r *http.Request) {
	opts := client.StartWorkflowOptions{
		ID:        pipeline.ManualNotifyWorkflowID(time.Now()),
		TaskQueue: config.TaskQueue,
	}
	we, err := h.Temporal.ExecuteWorkflow(r.Context(), opts, pipeline.NotifyTick)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "workflow_started",
		"workflow_id": we.GetID(),
	})
}

func parseForce(r *http.Request) bool {
	v := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("force")))
	return v == "1" || v == "true"
}

// POST /api/v0/scrape -> run a scrape synchronously (bypasses Temporal).
func (h *Handler) Scrape(w http.ResponseWriter, r *http.Request) {
	raw := queryDefault(r, "job_source", "linkedin")
	source, err := db.ParseJobSource(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	result, _ := h.Scraper.RunFullScrape(r.Context(), source)
	if result.Status == "error" {
		writeErr(w, http.StatusInternalServerError, result.Error)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// POST /api/v0/test
func (h *Handler) Test(w http.ResponseWriter, r *http.Request) {
	slog.Info("TEST was successful")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GET /api/v0/health
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "healthy", "timestamp": now()})
}

// GET /api/v0/db-test
func (h *Handler) DBTest(w http.ResponseWriter, r *http.Request) {
	settings, err := db.GetSearchSettings(r.Context(), h.DB)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Database error: "+err.Error())
		return
	}
	count := 0
	if settings != nil {
		count = 1
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":                "connected",
		"search_settings_count": count,
		"timestamp":             now(),
	})
}

// GET /api/v0/search-settings
func (h *Handler) SearchSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := db.GetSearchSettings(r.Context(), h.DB)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if settings == nil {
		writeJSON(w, http.StatusOK, map[string]string{"message": "No universal search settings found"})
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// GET /api/v0/scraper-settings
func (h *Handler) ScraperSettings(w http.ResponseWriter, r *http.Request) {
	raw := queryDefault(r, "job_source", "linkedin")
	source, err := db.ParseJobSource(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	settings, err := db.GetScraperSettings(r.Context(), h.DB, source)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if settings == nil {
		writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("No scraper settings found for %s", source)})
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// GET /api/v0/scraper-settings/all
func (h *Handler) AllScraperSettings(w http.ResponseWriter, r *http.Request) {
	all, err := db.AllScraperSettings(r.Context(), h.DB)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(all) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"message": "No scraper settings found"})
		return
	}
	out := make(map[string]db.ScraperSettings, len(all))
	for _, s := range all {
		out[string(s.JobSource)] = s
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /api/v0/jobs
func (h *Handler) Jobs(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 10)
	offset := queryInt(r, "offset", 0)
	jobs, err := db.ListJobs(r.Context(), h.DB, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

// GET /api/v0/jobs/stats
func (h *Handler) JobStats(w http.ResponseWriter, r *http.Request) {
	stats, err := db.GetJobStats(r.Context(), h.DB)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total_jobs":    stats.TotalJobs,
		"new_jobs":      stats.NewJobs,
		"relevant_jobs": stats.RelevantJobs,
		"by_state":      stats.ByState,
		"timestamp":     now(),
	})
}

// GET /api/v0/temporal-test
func (h *Handler) TemporalTest(w http.ResponseWriter, r *http.Request) {
	if _, err := h.Temporal.CheckHealth(r.Context(), &client.CheckHealthRequest{}); err != nil {
		writeErr(w, http.StatusInternalServerError, "Temporal error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":           "connected",
		"temporal_address": h.Cfg.TemporalAddress,
	})
}

// GET /api/v0/workflow/{id}
func (h *Handler) WorkflowStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	desc, err := h.Temporal.DescribeWorkflowExecution(r.Context(), id, "")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	info := desc.GetWorkflowExecutionInfo()
	writeJSON(w, http.StatusOK, map[string]any{
		"workflow_id": id,
		"status":      info.GetStatus().String(),
		"run_id":      info.GetExecution().GetRunId(),
	})
}

// GET /api/v0/config
func (h *Handler) Config(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"project_name":     h.Cfg.ProjectName,
		"version":          h.Cfg.Version,
		"database_url":     h.Cfg.RedactedDatabaseURL(),
		"temporal_address": h.Cfg.TemporalAddress,
		"log_level":        h.Cfg.LogLevel,
	})
}

func queryDefault(r *http.Request, key, def string) string {
	if v := r.URL.Query().Get(key); v != "" {
		return v
	}
	return def
}

func queryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
