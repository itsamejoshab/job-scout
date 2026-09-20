package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/domain"
	"github.com/jobscout/jobscout/internal/pipeline"
	"github.com/jobscout/jobscout/internal/scraper"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

// temporalAPI is the subset of client.Client used by HTTP handlers.
type temporalAPI interface {
	pipeline.SignalStarter
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error)
	DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error)
	CheckHealth(ctx context.Context, request *client.CheckHealthRequest) (*client.CheckHealthResponse, error)
	TerminateWorkflow(ctx context.Context, workflowID string, runID string, reason string, details ...interface{}) error
}

// Handler carries the dependencies shared by all HTTP handlers.
type Handler struct {
	DB        *sql.DB
	Temporal  temporalAPI
	Schedules pipeline.ScheduleStore
	Scraper   *scraper.Service
	Cfg       config.Config
}

// InstallSchedules creates or updates scrape and notify Temporal schedules.
func (h *Handler) InstallSchedules(ctx context.Context) error {
	return pipeline.EnsureSchedules(ctx, h.Schedules, h.Cfg)
}

// StartDispatchers terminates the legacy process-pending singleton, then
// signal-with-starts the fast-filter and detail dispatchers. A dispatcher that
// already runs is left alone.
func (h *Handler) StartDispatchers(ctx context.Context) error {
	if h.Temporal != nil {
		if err := h.Temporal.TerminateWorkflow(
			ctx, pipeline.LegacyProcessPendingWorkflowID, "",
			"replaced by filter-pending and process-pending",
		); err != nil {
			var notFound *serviceerror.NotFound
			if !errors.As(err, &notFound) {
				slog.Error("terminate legacy process-pending failed", "err", err)
			}
		}
	}
	if err := pipeline.WakeFilterPending(ctx, h.Temporal); err != nil {
		return err
	}
	return pipeline.WakeProcessPending(ctx, h.Temporal)
}

// wakeFilterPending drains pending rows soon instead of at the next idle poll.
// A failed wake must not fail the request that caused it.
func (h *Handler) wakeFilterPending(ctx context.Context) {
	if h.Temporal == nil {
		return
	}
	if err := pipeline.WakeFilterPending(ctx, h.Temporal); err != nil {
		slog.Error("wake filter-pending failed", "err", err)
	}
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
	we, err := h.Temporal.ExecuteWorkflow(r.Context(), opts, pipeline.ScrapeWorkflow, in)
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

// POST /api/v0/notify -> start NotifyWorkflow asynchronously.
func (h *Handler) Notify(w http.ResponseWriter, r *http.Request) {
	opts := client.StartWorkflowOptions{
		ID:        pipeline.ManualNotifyWorkflowID(time.Now()),
		TaskQueue: config.TaskQueue,
	}
	we, err := h.Temporal.ExecuteWorkflow(r.Context(), opts, pipeline.NotifyWorkflow)
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
	h.wakeFilterPending(r.Context())
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

type dependencyStatus struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// GET /api/v0/status
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	databaseStatus := dependencyStatus{OK: true}
	if err := h.DB.PingContext(r.Context()); err != nil {
		databaseStatus.OK = false
		databaseStatus.Error = err.Error()
	}

	temporalStatus := dependencyStatus{OK: true}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if _, err := h.Temporal.CheckHealth(ctx, &client.CheckHealthRequest{}); err != nil {
		temporalStatus.OK = false
		temporalStatus.Error = err.Error()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       databaseStatus.OK && temporalStatus.OK,
		"database": databaseStatus,
		"temporal": temporalStatus,
	})
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

type replaceSearchSettingsRequest struct {
	DescIncludeWords *[]string `json:"desc_include_words"`
	DescExcludeWords *[]string `json:"desc_exclude_words"`
	TitleInclude     *[]string `json:"title_include"`
	TitleExclude     *[]string `json:"title_exclude"`
	CompanyExclude   *[]string `json:"company_exclude"`
}

func (in replaceSearchSettingsRequest) validate() error {
	required := map[string]*[]string{
		"desc_include_words": in.DescIncludeWords,
		"desc_exclude_words": in.DescExcludeWords,
		"title_include":      in.TitleInclude,
		"title_exclude":      in.TitleExclude,
		"company_exclude":    in.CompanyExclude,
	}
	for field, value := range required {
		if value == nil {
			return fmt.Errorf("missing required field: %s", field)
		}
	}
	return nil
}

func cloneList(in []string) []string {
	return append([]string(nil), in...)
}

// PUT /api/v0/search-settings
func (h *Handler) ReplaceSearchSettings(w http.ResponseWriter, r *http.Request) {
	var in replaceSearchSettingsRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid search-settings body: "+err.Error())
		return
	}
	if err := in.validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	stored, err := db.ReplaceSearchSettings(r.Context(), h.DB, db.SearchSettings{
		DescIncludeWords: cloneList(*in.DescIncludeWords),
		DescExcludeWords: cloneList(*in.DescExcludeWords),
		TitleInclude:     cloneList(*in.TitleInclude),
		TitleExclude:     cloneList(*in.TitleExclude),
		CompanyExclude:   cloneList(*in.CompanyExclude),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

// POST /api/v0/search-settings/reset
func (h *Handler) ResetSearchSettings(w http.ResponseWriter, r *http.Request) {
	stored, err := db.ResetSearchSettings(r.Context(), h.DB)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stored)
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

type providerSearchQuery struct {
	Keywords string `json:"keywords"`
	Location string `json:"location"`
	Remote   string `json:"f_WT"`
}

type replaceScraperSettingsRequest struct {
	Enabled               *bool                  `json:"enabled"`
	ScrapeIntervalSeconds *int                   `json:"scrape_interval_seconds"`
	TimespanCode          *string                `json:"timespan_code"`
	PagesToScrape         *int                   `json:"pages_to_scrape"`
	Rounds                *int                   `json:"rounds"`
	SearchQueries         *[]providerSearchQuery `json:"search_queries"`
	GlobalSearches        *[]string              `json:"global_searches"`
}

func (in replaceScraperSettingsRequest) validate() error {
	switch {
	case in.Enabled == nil:
		return errors.New("missing required field: enabled")
	case in.ScrapeIntervalSeconds == nil:
		return errors.New("missing required field: scrape_interval_seconds")
	case *in.ScrapeIntervalSeconds < 60:
		return errors.New("scrape_interval_seconds must be at least 60")
	case in.TimespanCode == nil:
		return errors.New("missing required field: timespan_code")
	case strings.TrimSpace(*in.TimespanCode) == "":
		return errors.New("timespan_code must not be empty")
	case in.PagesToScrape == nil:
		return errors.New("missing required field: pages_to_scrape")
	case *in.PagesToScrape < 1:
		return errors.New("pages_to_scrape must be at least 1")
	case in.Rounds == nil:
		return errors.New("missing required field: rounds")
	case *in.Rounds < 1 || *in.Rounds > 3:
		return errors.New("rounds must be between 1 and 3")
	case in.SearchQueries == nil:
		return errors.New("missing required field: search_queries")
	case in.GlobalSearches == nil:
		return errors.New("missing required field: global_searches")
	}
	for i, query := range *in.SearchQueries {
		if strings.TrimSpace(query.Keywords) == "" {
			return fmt.Errorf("search_queries[%d].keywords must not be empty", i)
		}
		if strings.TrimSpace(query.Location) == "" {
			return fmt.Errorf("search_queries[%d].location must not be empty", i)
		}
	}
	for i, keywords := range *in.GlobalSearches {
		if strings.TrimSpace(keywords) == "" {
			return fmt.Errorf("global_searches[%d] must not be empty", i)
		}
	}
	return nil
}

func (in replaceScraperSettingsRequest) settings() db.ScraperSettings {
	queries := make([]map[string]string, 0, len(*in.SearchQueries))
	for _, query := range *in.SearchQueries {
		queries = append(queries, map[string]string{
			"keywords": strings.TrimSpace(query.Keywords),
			"location": strings.TrimSpace(query.Location),
			"f_WT":     strings.TrimSpace(query.Remote),
		})
	}
	global := make([]string, 0, len(*in.GlobalSearches))
	for _, keywords := range *in.GlobalSearches {
		global = append(global, strings.TrimSpace(keywords))
	}
	return db.ScraperSettings{
		Enabled:               *in.Enabled,
		ScrapeIntervalSeconds: *in.ScrapeIntervalSeconds,
		TimespanCode:          strings.TrimSpace(*in.TimespanCode),
		PagesToScrape:         *in.PagesToScrape,
		Rounds:                *in.Rounds,
		SearchQueries:         queries,
		GlobalSearches:        global,
	}
}

func scraperSourceFromPath(w http.ResponseWriter, r *http.Request) (db.JobSource, bool) {
	source, err := db.ParseJobSource(r.PathValue("job_source"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "job source not found")
		return "", false
	}
	return source, true
}

// PUT /api/v0/scraper-settings/{job_source}
func (h *Handler) ReplaceScraperSettings(w http.ResponseWriter, r *http.Request) {
	source, ok := scraperSourceFromPath(w, r)
	if !ok {
		return
	}
	existing, err := db.GetScraperSettings(r.Context(), h.DB, source)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		writeErr(w, http.StatusNotFound, "scraper settings not found")
		return
	}

	var in replaceScraperSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid scraper-settings body: "+err.Error())
		return
	}
	if err := in.validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if *in.Enabled && !isProviderImplemented(source) {
		writeErr(w, http.StatusBadRequest, "provider scraper is not implemented")
		return
	}
	stored, err := db.ReplaceScraperSettings(r.Context(), h.DB, source, in.settings())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if stored == nil {
		writeErr(w, http.StatusNotFound, "scraper settings not found")
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

// POST /api/v0/scraper-settings/{job_source}/reset
func (h *Handler) ResetScraperSettings(w http.ResponseWriter, r *http.Request) {
	source, ok := scraperSourceFromPath(w, r)
	if !ok {
		return
	}
	existing, err := db.GetScraperSettings(r.Context(), h.DB, source)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		writeErr(w, http.StatusNotFound, "scraper settings not found")
		return
	}
	stored, err := db.ResetScraperSettings(r.Context(), h.DB, source)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if stored == nil {
		writeErr(w, http.StatusNotFound, "scraper settings seed not found")
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

// GET /api/v0/jobs
func (h *Handler) Jobs(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 50)
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := queryInt(r, "offset", 0)
	if offset < 0 {
		offset = 0
	}

	var source db.JobSource
	var err error
	if raw := r.URL.Query().Get("job_source"); raw != "" {
		source, err = db.ParseJobSource(raw)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	for _, key := range []string{"date_from", "date_to"} {
		if raw := r.URL.Query().Get(key); raw != "" {
			if _, err := time.Parse(time.DateOnly, raw); err != nil {
				writeErr(w, http.StatusBadRequest, key+" must use YYYY-MM-DD")
				return
			}
		}
	}
	lastHours := 0
	if raw := r.URL.Query().Get("last_hours"); raw != "" {
		lastHours, err = strconv.Atoi(raw)
		if err != nil || lastHours < 0 {
			writeErr(w, http.StatusBadRequest, "last_hours must be a non-negative integer")
			return
		}
	}
	dateFrom := r.URL.Query().Get("date_from")
	dateTo := r.URL.Query().Get("date_to")
	if lastHours > 0 && (dateFrom != "" || dateTo != "") {
		writeErr(w, http.StatusBadRequest, "last_hours cannot be combined with date_from or date_to")
		return
	}

	var anchor time.Time
	if raw := r.URL.Query().Get("as_of"); raw != "" {
		anchor, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "as_of must be an RFC3339 timestamp")
			return
		}
	} else if err := h.DB.QueryRowContext(r.Context(), "SELECT CURRENT_TIMESTAMP").Scan(&anchor); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	timezone := h.Cfg.ReportingTimezone
	if _, err := time.LoadLocation(timezone); err != nil {
		timezone = "America/New_York"
	}
	jobs, total, err := db.ListJobsPage(r.Context(), h.DB, db.JobListFilter{
		State:     strings.TrimSpace(r.URL.Query().Get("state")),
		JobSource: source,
		Query:     strings.TrimSpace(r.URL.Query().Get("q")),
		DateFrom:  dateFrom,
		DateTo:    dateTo,
		LastHours: lastHours,
		AsOf:      anchor,
		Timezone:  timezone,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]jobListItem, 0, len(jobs))
	for _, job := range jobs {
		items = append(items, newJobListItem(job))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": total,
		"as_of": anchor,
	})
}

const jobDescriptionPreviewLimit = 480

type jobListItem struct {
	ID                 int64        `json:"id"`
	JobSource          db.JobSource `json:"job_source"`
	Title              string       `json:"title"`
	Company            string       `json:"company"`
	HasDescription     bool         `json:"has_description"`
	DescriptionPreview string       `json:"description_preview"`
	Location           string       `json:"location"`
	Date               time.Time    `json:"date"`
	JobURL             string       `json:"job_url"`
	CreatedAt          time.Time    `json:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at"`
	New                bool         `json:"new"`
	Duplicate          bool         `json:"duplicate"`
	Relevant           bool         `json:"relevant"`
	Promising          bool         `json:"promising"`
	Notified           bool         `json:"notified"`
	State              string       `json:"state"`
	RejectReason       *string      `json:"reject_reason"`
	IsRemote           bool         `json:"is_remote"`
	DetailAttempts     int          `json:"detail_attempts"`
	StateChangedAt     time.Time    `json:"state_changed_at"`
	NotifiedAt         *time.Time   `json:"notified_at"`
}

func descriptionPreview(description *string) string {
	if description == nil {
		return ""
	}
	collapsed := strings.Join(strings.Fields(*description), " ")
	if collapsed == "" {
		return ""
	}
	runes := []rune(collapsed)
	if len(runes) <= jobDescriptionPreviewLimit {
		return collapsed
	}
	return string(runes[:jobDescriptionPreviewLimit-1]) + "…"
}

func newJobListItem(job db.Job) jobListItem {
	preview := descriptionPreview(job.Description)
	return jobListItem{
		ID: job.ID, JobSource: job.JobSource, Title: job.Title, Company: job.Company,
		HasDescription: preview != "", DescriptionPreview: preview,
		Location: job.Location, Date: job.Date, JobURL: job.JobURL, CreatedAt: job.CreatedAt,
		UpdatedAt: job.UpdatedAt, New: job.New, Duplicate: job.Duplicate,
		Relevant: job.Relevant, Promising: job.Promising, Notified: job.Notified,
		State: job.State, RejectReason: job.RejectReason, IsRemote: job.IsRemote,
		DetailAttempts: job.DetailAttempts, StateChangedAt: job.StateChangedAt,
		NotifiedAt: job.NotifiedAt,
	}
}

// GET /api/v0/jobs/{id}
func (h *Handler) Job(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "job id must be numeric")
		return
	}
	job, err := db.GetJob(r.Context(), h.DB, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, job)
}

type reviewJobRequest struct {
	Action string `json:"action"`
}

// POST /api/v0/jobs/{id}/review
func (h *Handler) ReviewJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "job id must be numeric")
		return
	}
	var in reviewJobRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid review body")
		return
	}
	if in.Action != db.JobStateApplied && in.Action != db.JobStateDismissed {
		writeErr(w, http.StatusBadRequest, "action must be applied or dismissed")
		return
	}
	updated, err := db.ReviewReadyJob(r.Context(), h.DB, id, in.Action)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !updated {
		if _, err := db.GetJob(r.Context(), h.DB, id); errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "job not found")
			return
		} else if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeErr(w, http.StatusConflict, "job is not ready for review")
		return
	}
	job, err := db.GetJob(r.Context(), h.DB, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// POST /api/v0/jobs/re-evaluate
func (h *Handler) ReEvaluateJobs(w http.ResponseWriter, r *http.Request) {
	updated, err := db.ReEvaluateRejectedJobs(r.Context(), h.DB)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if updated > 0 {
		h.wakeFilterPending(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": updated})
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

type dashboardProviderView struct {
	JobSource             string         `json:"job_source"`
	Implemented           bool           `json:"implemented"`
	Enabled               bool           `json:"enabled"`
	ScrapeIntervalSeconds int            `json:"scrape_interval_seconds"`
	LastScrapedAt         *time.Time     `json:"last_scraped_at"`
	NextEligibleAt        *time.Time     `json:"next_eligible_at"`
	Status                string         `json:"status"`
	TotalJobs             int            `json:"total_jobs"`
	ByState               map[string]int `json:"by_state"`
	ByRejectReason        map[string]int `json:"by_reject_reason"`
}

type dashboardDailyView struct {
	Day      string `json:"day"`
	Total    int    `json:"total"`
	Notified int    `json:"notified"`
}

// GET /api/v0/dashboard/stats
func (h *Handler) DashboardStats(w http.ResponseWriter, r *http.Request) {
	stats, err := db.GetDashboardStats(r.Context(), h.DB, h.Cfg.ReportingTimezone)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	nowTime := time.Now()
	providers := make([]dashboardProviderView, 0, len(stats.Providers))
	for _, provider := range stats.Providers {
		status := "disabled"
		if provider.Enabled {
			cadence := domain.ProviderCadence{
				Source:                string(provider.JobSource),
				Enabled:               provider.Enabled,
				ScrapeIntervalSeconds: provider.ScrapeIntervalSeconds,
				LastScrapedAt:         provider.LastScrapedAt,
				NextEligibleAt:        provider.NextEligibleAt,
			}
			if domain.IsDue(cadence, nowTime, false) {
				status = "due"
			} else {
				status = "waiting"
			}
		}

		providers = append(providers, dashboardProviderView{
			JobSource:             string(provider.JobSource),
			Implemented:           isProviderImplemented(provider.JobSource),
			Enabled:               provider.Enabled,
			ScrapeIntervalSeconds: provider.ScrapeIntervalSeconds,
			LastScrapedAt:         provider.LastScrapedAt,
			NextEligibleAt:        provider.NextEligibleAt,
			Status:                status,
			TotalJobs:             provider.TotalJobs,
			ByState:               provider.ByState,
			ByRejectReason:        provider.ByRejectReason,
		})
	}

	daily := make([]dashboardDailyView, 0, len(stats.Daily))
	for _, point := range stats.Daily {
		daily = append(daily, dashboardDailyView{
			Day:      point.Day,
			Total:    point.Total,
			Notified: point.Notified,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at": now(),
		"timezone":     stats.Timezone,
		"notified":     stats.Notified,
		"providers":    providers,
		"daily":        daily,
	})
}

func isProviderImplemented(source db.JobSource) bool {
	switch source {
	case db.SourceLinkedIn:
		return true
	case db.SourceIndeed:
		return false
	default:
		return false
	}
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
		"project_name":        h.Cfg.ProjectName,
		"version":             h.Cfg.Version,
		"database_url":        h.Cfg.RedactedDatabaseURL(),
		"temporal_address":    h.Cfg.TemporalAddress,
		"temporal_ui_address": h.Cfg.TemporalUIAddress,
		"reporting_timezone":  h.Cfg.ReportingTimezone,
		"log_level":           h.Cfg.LogLevel,
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
