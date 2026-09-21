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
	"sync"
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

	apifyBudgetOnce  sync.Once
	apifyBudgetCache *scraper.BudgetViewCache
}

// InstallSchedules creates or updates scrape and notify Temporal schedules.
func (h *Handler) InstallSchedules(ctx context.Context) error {
	notification, err := h.notificationSchedule(ctx)
	if err != nil {
		return err
	}
	return pipeline.EnsureSchedulesWithNotification(ctx, h.Schedules, h.Cfg, notification)
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
		if isApifyProvider(source) && strings.TrimSpace(h.Cfg.ApifyAPIToken) == "" {
			writeJSON(w, http.StatusOK, map[string]any{
				"status":     "provider_disabled",
				"reason":     scraper.ApifySetupMessage,
				"configured": false,
			})
			return
		}
		in.JobSource = string(source)
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
	status, err := h.notificationStatus(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !status.Active {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "notifications_disabled",
			"reason":     status.Reason,
			"enabled":    status.Enabled,
			"configured": status.Configured,
			"active":     false,
		})
		return
	}

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
	OnsiteKeywords   *[]string `json:"onsite_keywords"`
	RemoteKeywords   *[]string `json:"remote_keywords"`
	HybridKeywords   *[]string `json:"hybrid_keywords"`
}

func (in replaceSearchSettingsRequest) validate() error {
	required := map[string]*[]string{
		"desc_include_words": in.DescIncludeWords,
		"desc_exclude_words": in.DescExcludeWords,
		"title_include":      in.TitleInclude,
		"title_exclude":      in.TitleExclude,
		"company_exclude":    in.CompanyExclude,
		"onsite_keywords":    in.OnsiteKeywords,
		"remote_keywords":    in.RemoteKeywords,
		"hybrid_keywords":    in.HybridKeywords,
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
		OnsiteKeywords:   cloneList(*in.OnsiteKeywords),
		RemoteKeywords:   cloneList(*in.RemoteKeywords),
		HybridKeywords:   cloneList(*in.HybridKeywords),
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

type notificationSettingsView struct {
	Enabled    bool                          `json:"enabled"`
	Configured bool                          `json:"configured"`
	Active     bool                          `json:"active"`
	Reason     string                        `json:"reason,omitempty"`
	TimeZone   string                        `json:"timezone"`
	Schedule   pipeline.NotificationSchedule `json:"schedule"`
}

func (h *Handler) notificationStatus(ctx context.Context) (pipeline.NotificationStatus, error) {
	enabled := true
	if h.DB != nil {
		var err error
		enabled, err = db.GetNotificationsEnabled(ctx, h.DB)
		if err != nil {
			return pipeline.NotificationStatus{}, err
		}
	}
	return pipeline.ResolveNotificationStatus(enabled, h.Cfg.NotificationsConfigured()), nil
}

func (h *Handler) notificationSchedule(ctx context.Context) (pipeline.NotificationSchedule, error) {
	fallback := pipeline.DefaultNotificationSchedule(h.Cfg)
	if h.DB == nil {
		return fallback, nil
	}
	stored, err := db.GetNotificationScheduleSettings(ctx, h.DB)
	if err != nil {
		return pipeline.NotificationSchedule{}, err
	}
	if stored == nil {
		return fallback, nil
	}
	periods := make([]pipeline.SilentPeriod, len(stored.SilentPeriods))
	for i, period := range stored.SilentPeriods {
		periods[i] = pipeline.SilentPeriod{
			Days:  append([]int(nil), period.Days...),
			Start: period.Start,
			End:   period.End,
		}
	}
	return pipeline.NotificationSchedule{
		Mode:            stored.Mode,
		IntervalMinutes: stored.IntervalMinutes,
		CronPattern:     stored.CronPattern,
		SilentPeriods:   periods,
	}, nil
}

func storeNotificationSchedule(
	ctx context.Context,
	database *sql.DB,
	settings pipeline.NotificationSchedule,
) error {
	periods := make([]db.SilentPeriod, len(settings.SilentPeriods))
	for i, period := range settings.SilentPeriods {
		periods[i] = db.SilentPeriod{
			Days:  append([]int(nil), period.Days...),
			Start: period.Start,
			End:   period.End,
		}
	}
	_, err := db.ReplaceNotificationScheduleSettings(ctx, database, db.NotificationScheduleSettings{
		Mode:            settings.Mode,
		IntervalMinutes: settings.IntervalMinutes,
		CronPattern:     settings.CronPattern,
		SilentPeriods:   periods,
	})
	return err
}

func notificationTimeZone(cfg config.Config) string {
	if cfg.ReportingTimezone != "" {
		return cfg.ReportingTimezone
	}
	return "America/New_York"
}

func (h *Handler) writeNotificationSettings(
	ctx context.Context,
	w http.ResponseWriter,
	status pipeline.NotificationStatus,
) {
	schedule, err := h.notificationSchedule(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, notificationSettingsView{
		Enabled:    status.Enabled,
		Configured: status.Configured,
		Active:     status.Active,
		Reason:     status.Reason,
		TimeZone:   notificationTimeZone(h.Cfg),
		Schedule:   schedule,
	})
}

// GET /api/v0/notification-settings
func (h *Handler) NotificationSettings(w http.ResponseWriter, r *http.Request) {
	status, err := h.notificationStatus(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.writeNotificationSettings(r.Context(), w, status)
}

type replaceNotificationSettingsRequest struct {
	Enabled  *bool                          `json:"enabled"`
	Schedule *pipeline.NotificationSchedule `json:"schedule"`
}

// PUT /api/v0/notification-settings
func (h *Handler) ReplaceNotificationSettings(w http.ResponseWriter, r *http.Request) {
	var in replaceNotificationSettingsRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid notification-settings body: "+err.Error())
		return
	}
	if in.Enabled == nil {
		writeErr(w, http.StatusBadRequest, "missing required field: enabled")
		return
	}
	if in.Schedule != nil {
		normalized, err := pipeline.NormalizeNotificationSchedule(*in.Schedule)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := pipeline.EnsureNotificationSchedule(
			r.Context(),
			h.Schedules,
			h.Cfg,
			normalized,
		); err != nil {
			writeErr(w, http.StatusInternalServerError, "update notification schedule: "+err.Error())
			return
		}
		if err := storeNotificationSchedule(r.Context(), h.DB, normalized); err != nil {
			writeErr(w, http.StatusInternalServerError, "store notification schedule: "+err.Error())
			return
		}
	}
	if _, err := db.SetNotificationsEnabled(r.Context(), h.DB, *in.Enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	status, err := h.notificationStatus(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.writeNotificationSettings(r.Context(), w, status)
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
	if !isApifyProvider(source) {
		writeJSON(w, http.StatusOK, settings)
		return
	}
	budget := h.cachedApifyBudget(r.Context())
	writeJSON(w, http.StatusOK, scraperSettingsView{
		ScraperSettings: *settings,
		ApifyBudget:     &budget,
	})
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
	Keywords      string
	Location      string
	Remote        string
	Radius        string
	IncludeRemote *bool
	IncludeHybrid *bool
	hasWorkType   bool
	hasRadius     bool
}

func (q *providerSearchQuery) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if err := unmarshalJSONString(raw["keywords"], &q.Keywords); err != nil {
		return err
	}
	if err := unmarshalJSONString(raw["location"], &q.Location); err != nil {
		return err
	}
	if _, ok := raw["f_WT"]; ok {
		q.hasWorkType = true
		if err := unmarshalJSONString(raw["f_WT"], &q.Remote); err != nil {
			return err
		}
	}
	if _, ok := raw["radius"]; ok {
		q.hasRadius = true
		if err := unmarshalJSONString(raw["radius"], &q.Radius); err != nil {
			return err
		}
	}
	if rawRemote, ok := raw["include_remote"]; ok && string(rawRemote) != "null" {
		var flag bool
		if err := json.Unmarshal(rawRemote, &flag); err != nil {
			return fmt.Errorf("include_remote must be a JSON boolean")
		}
		q.IncludeRemote = &flag
	}
	if rawHybrid, ok := raw["include_hybrid"]; ok && string(rawHybrid) != "null" {
		var flag bool
		if err := json.Unmarshal(rawHybrid, &flag); err != nil {
			return fmt.Errorf("include_hybrid must be a JSON boolean")
		}
		q.IncludeHybrid = &flag
	}
	return nil
}

func unmarshalJSONString(raw json.RawMessage, dest *string) error {
	if len(raw) == 0 || string(raw) == "null" {
		*dest = ""
		return nil
	}
	return json.Unmarshal(raw, dest)
}

type replaceScraperSettingsRequest struct {
	Enabled               *bool                  `json:"enabled"`
	ScrapeIntervalSeconds *int                   `json:"scrape_interval_seconds"`
	TimespanCode          *string                `json:"timespan_code"`
	PagesToScrape         *int                   `json:"pages_to_scrape"`
	Rounds                *int                   `json:"rounds"`
	SearchQueries         *[]providerSearchQuery `json:"search_queries"`
	GlobalSearches        *[]string              `json:"global_searches"`
	ProviderOptions       *map[string]any        `json:"provider_options"`
}

func (in replaceScraperSettingsRequest) validate(source db.JobSource) error {
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
	switch source {
	case db.SourceDice:
		return in.validateDice()
	case db.SourceIndeed:
		return in.validateIndeed()
	case db.SourceFantastic:
		return in.validateFantastic()
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

func (in replaceScraperSettingsRequest) validateDice() error {
	timespan := strings.TrimSpace(*in.TimespanCode)
	allowedTimespan := map[string]struct{}{
		"all": {}, "24h": {}, "3d": {}, "7d": {}, "30d": {},
	}
	if _, ok := allowedTimespan[timespan]; !ok {
		return errors.New("timespan_code must be one of all, 24h, 3d, 7d, 30d")
	}
	if *in.PagesToScrape > 5 {
		return errors.New("pages_to_scrape must not be greater than 5")
	}
	for _, keywords := range *in.GlobalSearches {
		if strings.TrimSpace(keywords) != "" {
			return errors.New("Dice global_searches must be empty")
		}
	}
	queries := map[string]struct{}{}
	locations := map[string]struct{}{}
	for i, query := range *in.SearchQueries {
		if query.hasWorkType {
			return errors.New("Dice search_queries must not include f_WT")
		}
		if query.IncludeRemote == nil {
			return fmt.Errorf("search_queries[%d].include_remote must be a JSON boolean", i)
		}
		if strings.TrimSpace(query.Keywords) == "" {
			return fmt.Errorf("search_queries[%d].keywords must not be empty", i)
		}
		if strings.TrimSpace(query.Location) == "" {
			return fmt.Errorf("search_queries[%d].location must not be empty", i)
		}
		queries[strings.TrimSpace(query.Keywords)] = struct{}{}
		locations[strings.TrimSpace(query.Location)] = struct{}{}
	}
	if len(queries) > 10 {
		return errors.New("Dice search queries must not exceed 10")
	}
	if len(locations) > 10 {
		return errors.New("Dice locations must not exceed 10")
	}
	if len(*in.SearchQueries) > 20 {
		return errors.New("Dice expanded search pairs must not exceed 20")
	}
	return nil
}

func (in replaceScraperSettingsRequest) validateIndeed() error {
	if in.ProviderOptions == nil {
		return errors.New("missing required field: provider_options")
	}
	opts, err := db.ParseIndeedOptions(*in.ProviderOptions)
	if err != nil {
		return errors.New("provider_options must be a valid Indeed options object")
	}
	allowedCountry := map[string]struct{}{
		"ar": {}, "au": {}, "at": {}, "bh": {}, "be": {}, "br": {}, "ca": {}, "cl": {}, "cn": {}, "co": {},
		"cz": {}, "dk": {}, "fi": {}, "fr": {}, "de": {}, "gr": {}, "hk": {}, "hu": {}, "in": {}, "id": {},
		"ie": {}, "il": {}, "it": {}, "jp": {}, "kw": {}, "lu": {}, "my": {}, "mx": {}, "ma": {}, "nl": {},
		"nz": {}, "no": {}, "om": {}, "pe": {}, "ph": {}, "pl": {}, "pt": {}, "qa": {}, "ro": {}, "sa": {},
		"sg": {}, "za": {}, "kr": {}, "es": {}, "se": {}, "ch": {}, "tw": {}, "tr": {}, "ua": {}, "ae": {},
		"uk": {}, "us": {}, "ve": {}, "vn": {}, "cr": {}, "ec": {}, "eg": {}, "ng": {}, "pk": {}, "pa": {},
		"th": {}, "uy": {},
	}
	if _, ok := allowedCountry[strings.ToLower(strings.TrimSpace(opts.Country))]; !ok {
		return errors.New("provider_options.country must be a supported Indeed country code")
	}
	allowedJobType := map[string]struct{}{
		"fulltime": {}, "parttime": {}, "contract": {}, "internship": {},
		"temporary": {}, "permanent": {}, "seasonal": {}, "freelance": {},
	}
	if _, ok := allowedJobType[strings.TrimSpace(opts.JobType)]; !ok {
		return errors.New("provider_options.jobType must be one of fulltime, parttime, contract, internship, temporary, permanent, seasonal, freelance")
	}
	allowedFromDays := map[string]struct{}{"1": {}, "3": {}, "7": {}, "14": {}}
	if _, ok := allowedFromDays[strings.TrimSpace(opts.FromDays)]; !ok {
		return errors.New("provider_options.fromDays must be one of 1, 3, 7, 14")
	}
	if opts.MaxRows < 1 || opts.MaxRows > 1000 {
		return errors.New("provider_options.maxRows must be between 1 and 1000")
	}
	if strings.TrimSpace(*in.TimespanCode) != strings.TrimSpace(opts.FromDays) {
		return errors.New("timespan_code must match provider_options.fromDays")
	}
	for _, keywords := range *in.GlobalSearches {
		if strings.TrimSpace(keywords) != "" {
			return errors.New("Indeed global_searches must be empty")
		}
	}
	queries := map[string]struct{}{}
	locations := map[string]struct{}{}
	for i, query := range *in.SearchQueries {
		if query.hasWorkType {
			return errors.New("Indeed search_queries must not include f_WT")
		}
		if query.IncludeRemote == nil {
			return fmt.Errorf("search_queries[%d].include_remote must be a JSON boolean", i)
		}
		if query.IncludeHybrid == nil {
			return fmt.Errorf("search_queries[%d].include_hybrid must be a JSON boolean", i)
		}
		if !query.hasRadius || strings.TrimSpace(query.Radius) == "" {
			return fmt.Errorf("search_queries[%d].radius must be one of 0, 5, 10, 15, 25, 35, 50, 100", i)
		}
		if !db.ValidIndeedRadius(query.Radius) {
			return fmt.Errorf("search_queries[%d].radius must be one of 0, 5, 10, 15, 25, 35, 50, 100", i)
		}
		if strings.TrimSpace(query.Keywords) == "" {
			return fmt.Errorf("search_queries[%d].keywords must not be empty", i)
		}
		if strings.TrimSpace(query.Location) == "" {
			return fmt.Errorf("search_queries[%d].location must not be empty", i)
		}
		queries[strings.TrimSpace(query.Keywords)] = struct{}{}
		locations[strings.TrimSpace(query.Location)] = struct{}{}
	}
	if len(queries) > 10 {
		return errors.New("Indeed search queries must not exceed 10")
	}
	if len(locations) > 10 {
		return errors.New("Indeed locations must not exceed 10")
	}
	if len(*in.SearchQueries) > 20 {
		return errors.New("Indeed expanded search pairs must not exceed 20")
	}
	return nil
}

func (in replaceScraperSettingsRequest) validateFantastic() error {
	if in.ProviderOptions == nil {
		return errors.New("missing required field: provider_options")
	}
	opts, err := db.ParseFantasticOptions(*in.ProviderOptions)
	if err != nil {
		return errors.New("provider_options must be a valid Fantastic options object")
	}
	if strings.TrimSpace(*in.TimespanCode) != "all" {
		return errors.New("timespan_code must be all")
	}
	for _, keywords := range *in.GlobalSearches {
		if strings.TrimSpace(keywords) != "" {
			return errors.New("Fantastic global_searches must be empty")
		}
	}
	if len(opts.Queries) == 0 {
		return errors.New("provider_options.queries must include at least 1 query")
	}
	if len(opts.Queries) > db.FantasticMaxQueries {
		return fmt.Errorf("Fantastic queries must not exceed %d", db.FantasticMaxQueries)
	}
	for i, query := range opts.Queries {
		if len(query.TitleSearch) == 0 {
			return fmt.Errorf("provider_options.queries[%d].titleSearch must include at least 1 title", i)
		}
		if len(query.LocationSearch) == 0 {
			return fmt.Errorf("provider_options.queries[%d].locationSearch must include at least 1 location", i)
		}
		if query.Limit < db.FantasticMinLimit || query.Limit > db.FantasticMaxLimit {
			return fmt.Errorf("provider_options.queries[%d].limit must be between %d and %d", i, db.FantasticMinLimit, db.FantasticMaxLimit)
		}
		if len(query.AIWorkArrangementFilter) == 0 {
			return fmt.Errorf("provider_options.queries[%d].aiWorkArrangementFilter must include at least 1 value", i)
		}
		for _, value := range query.AIWorkArrangementFilter {
			if !db.ValidFantasticWorkArrangement(value) {
				return fmt.Errorf("provider_options.queries[%d].aiWorkArrangementFilter must be one or more of On-site, Hybrid, Remote OK, Remote Solely", i)
			}
		}
		if len(query.AIEmploymentTypeFilter) == 0 {
			return fmt.Errorf("provider_options.queries[%d].aiEmploymentTypeFilter must include at least 1 value", i)
		}
		for _, value := range query.AIEmploymentTypeFilter {
			if !db.ValidFantasticEmploymentType(value) {
				return fmt.Errorf("provider_options.queries[%d].aiEmploymentTypeFilter must be one or more of FULL_TIME, PART_TIME, CONTRACTOR, TEMPORARY, INTERN, VOLUNTEER, PER_DIEM, OTHER", i)
			}
		}
	}
	return nil
}

func (in replaceScraperSettingsRequest) settings(source db.JobSource) db.ScraperSettings {
	queries := make([]map[string]string, 0, len(*in.SearchQueries))
	for _, query := range *in.SearchQueries {
		item := map[string]string{
			"keywords": strings.TrimSpace(query.Keywords),
			"location": strings.TrimSpace(query.Location),
		}
		switch source {
		case db.SourceDice:
			item["include_remote"] = strconv.FormatBool(query.IncludeRemote != nil && *query.IncludeRemote)
		case db.SourceIndeed:
			item["include_remote"] = strconv.FormatBool(query.IncludeRemote != nil && *query.IncludeRemote)
			item["include_hybrid"] = strconv.FormatBool(query.IncludeHybrid != nil && *query.IncludeHybrid)
			item["radius"] = strings.TrimSpace(query.Radius)
		case db.SourceFantastic:
		default:
			item["f_WT"] = strings.TrimSpace(query.Remote)
		}
		queries = append(queries, item)
	}
	global := make([]string, 0, len(*in.GlobalSearches))
	for _, keywords := range *in.GlobalSearches {
		global = append(global, strings.TrimSpace(keywords))
	}
	options := map[string]any{}
	switch source {
	case db.SourceDice:
		global = []string{}
	case db.SourceIndeed:
		global = []string{}
		if in.ProviderOptions != nil {
			if parsed, err := db.ParseIndeedOptions(*in.ProviderOptions); err == nil {
				parsed.Country = strings.ToLower(strings.TrimSpace(parsed.Country))
				parsed.JobType = strings.TrimSpace(parsed.JobType)
				parsed.FromDays = strings.TrimSpace(parsed.FromDays)
				options = db.IndeedOptionsMap(parsed)
			}
		}
	case db.SourceFantastic:
		global = []string{}
		if in.ProviderOptions != nil {
			if parsed, err := db.ParseFantasticOptions(*in.ProviderOptions); err == nil {
				options = db.FantasticOptionsMap(parsed)
				queries = db.FantasticSearchQueries(parsed)
			}
		}
	}
	return db.ScraperSettings{
		Enabled:               *in.Enabled,
		ScrapeIntervalSeconds: *in.ScrapeIntervalSeconds,
		TimespanCode:          strings.TrimSpace(*in.TimespanCode),
		PagesToScrape:         *in.PagesToScrape,
		Rounds:                *in.Rounds,
		SearchQueries:         queries,
		GlobalSearches:        global,
		ProviderOptions:       options,
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
	if err := in.validate(source); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if *in.Enabled && !isProviderImplemented(source) {
		writeErr(w, http.StatusBadRequest, "provider scraper is not implemented")
		return
	}
	stored, err := db.ReplaceScraperSettings(r.Context(), h.DB, source, in.settings(source))
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
	SearchIntention    string       `json:"search_intention"`
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
		SearchIntention: job.SearchIntention,
		DetailAttempts:  job.DetailAttempts, StateChangedAt: job.StateChangedAt,
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
	var updated bool
	switch in.Action {
	case db.JobStateApplied, db.JobStateDismissed:
		updated, err = db.ReviewReadyJob(r.Context(), h.DB, id, in.Action)
	case db.JobStateReady:
		updated, err = db.ReturnJobToReview(r.Context(), h.DB, id)
	default:
		writeErr(w, http.StatusBadRequest, "action must be applied, dismissed, or ready")
		return
	}
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
		if in.Action == db.JobStateReady {
			writeErr(w, http.StatusConflict, "job cannot go back to review from this state")
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

type scraperSettingsView struct {
	db.ScraperSettings
	ApifyBudget *scraper.ApifyBudgetView `json:"apify_budget,omitempty"`
}

type dashboardProviderView struct {
	JobSource             string                   `json:"job_source"`
	Implemented           bool                     `json:"implemented"`
	Configured            bool                     `json:"configured"`
	ConfigurationMessage  string                   `json:"configuration_message,omitempty"`
	Enabled               bool                     `json:"enabled"`
	ScrapeIntervalSeconds int                      `json:"scrape_interval_seconds"`
	LastScrapedAt         *time.Time               `json:"last_scraped_at"`
	NextEligibleAt        *time.Time               `json:"next_eligible_at"`
	Status                string                   `json:"status"`
	TotalJobs             int                      `json:"total_jobs"`
	ByState               map[string]int           `json:"by_state"`
	ByRejectReason        map[string]int           `json:"by_reject_reason"`
	ApifyBudget           *scraper.ApifyBudgetView `json:"apify_budget,omitempty"`
}

type dashboardDailyView struct {
	Day      string `json:"day"`
	Total    int    `json:"total"`
	Notified int    `json:"notified"`
	Applied  int    `json:"applied"`
	Pending  int    `json:"pending"`
	Skipped  int    `json:"skipped"`
}

// GET /api/v0/dashboard/stats
func (h *Handler) DashboardStats(w http.ResponseWriter, r *http.Request) {
	stats, err := db.GetDashboardStats(r.Context(), h.DB, h.Cfg.ReportingTimezone)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	nowTime := time.Now()
	var apifyBudget *scraper.ApifyBudgetView
	for _, provider := range stats.Providers {
		if isApifyProvider(provider.JobSource) {
			view := h.cachedApifyBudget(r.Context())
			apifyBudget = &view
			break
		}
	}
	providers := make([]dashboardProviderView, 0, len(stats.Providers))
	for _, provider := range stats.Providers {
		configured := !isApifyProvider(provider.JobSource) || strings.TrimSpace(h.Cfg.ApifyAPIToken) != ""
		effectiveEnabled := provider.Enabled && configured
		status := "disabled"
		if !configured {
			status = "setup_required"
		} else if provider.Enabled {
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

		item := dashboardProviderView{
			JobSource:             string(provider.JobSource),
			Implemented:           isProviderImplemented(provider.JobSource),
			Configured:            configured,
			Enabled:               effectiveEnabled,
			ScrapeIntervalSeconds: provider.ScrapeIntervalSeconds,
			LastScrapedAt:         provider.LastScrapedAt,
			NextEligibleAt:        provider.NextEligibleAt,
			Status:                status,
			TotalJobs:             provider.TotalJobs,
			ByState:               provider.ByState,
			ByRejectReason:        provider.ByRejectReason,
		}
		if isApifyProvider(provider.JobSource) {
			item.ApifyBudget = apifyBudget
			if !configured {
				item.ConfigurationMessage = scraper.ApifySetupMessage
			}
		}
		providers = append(providers, item)
	}

	daily := make([]dashboardDailyView, 0, len(stats.Daily))
	for _, point := range stats.Daily {
		daily = append(daily, dashboardDailyView{
			Day:      point.Day,
			Total:    point.Total,
			Notified: point.Notified,
			Applied:  point.Applied,
			Pending:  point.Pending,
			Skipped:  point.Skipped,
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
	case db.SourceLinkedIn, db.SourceDice, db.SourceIndeed, db.SourceFantastic:
		return true
	default:
		return false
	}
}

func isApifyProvider(source db.JobSource) bool {
	return source == db.SourceDice || source == db.SourceIndeed || source == db.SourceFantastic
}

func (h *Handler) cachedApifyBudget(ctx context.Context) scraper.ApifyBudgetView {
	h.apifyBudgetOnce.Do(func() {
		h.apifyBudgetCache = &scraper.BudgetViewCache{}
	})
	return h.apifyBudgetCache.Get(h.apifyBudgetNow(), func() scraper.ApifyBudgetView {
		return scraper.DisplayBudget(ctx, h.apifyDisplayClient(), strings.TrimSpace(h.Cfg.ApifyAPIToken), h.Cfg.ApifyMonthlyBudgetCents)
	})
}

func (h *Handler) apifyBudgetNow() time.Time {
	if h.Scraper != nil && h.Scraper.Now != nil {
		return h.Scraper.Now()
	}
	return time.Now().UTC()
}

func (h *Handler) apifyDisplayClient() *scraper.ApifyClient {
	client := &scraper.ApifyClient{Token: strings.TrimSpace(h.Cfg.ApifyAPIToken)}
	if h.Scraper != nil {
		client.BaseURL = h.Scraper.ApifyBaseURL
		client.HTTP = h.Scraper.HTTPClient
		client.Now = h.Scraper.Now
		client.Sleep = h.Scraper.Sleep
	}
	return client
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
