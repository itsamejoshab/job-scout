package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
	"github.com/jobscout/jobscout/internal/scraper"
)

const dashboardStatsPath = "/api/v0/dashboard/stats"

type dashboardStatsBody struct {
	GeneratedAt string                   `json:"generated_at"`
	Timezone    string                   `json:"timezone"`
	Notified    int                      `json:"notified"`
	Providers   []dashboardProviderStats `json:"providers"`
	Daily       []dashboardDailyPoint    `json:"daily"`
}

type dashboardProviderStats struct {
	JobSource             string           `json:"job_source"`
	Implemented           bool             `json:"implemented"`
	Configured            bool             `json:"configured"`
	ConfigurationMessage  string           `json:"configuration_message"`
	Enabled               bool             `json:"enabled"`
	ScrapeIntervalSeconds int              `json:"scrape_interval_seconds"`
	LastScrapedAt         *string          `json:"last_scraped_at"`
	NextEligibleAt        *string          `json:"next_eligible_at"`
	Status                string           `json:"status"`
	TotalJobs             int              `json:"total_jobs"`
	ByState               map[string]int   `json:"by_state"`
	ByRejectReason        map[string]int   `json:"by_reject_reason"`
	ApifyBudget           *apifyBudgetJSON `json:"apify_budget"`
}

type apifyBudgetJSON struct {
	PeriodStart  string       `json:"period_start"`
	PeriodEnd    string       `json:"period_end"`
	LimitUSD     json.Number  `json:"limit_usd"`
	UsedUSD      *json.Number `json:"used_usd"`
	RemainingUSD *json.Number `json:"remaining_usd"`
	Blocked      bool         `json:"blocked"`
	Reason       string       `json:"reason"`
}

type dashboardDailyPoint struct {
	Day      string `json:"day"`
	Total    int    `json:"total"`
	Notified int    `json:"notified"`
	Applied  int    `json:"applied"`
	Pending  int    `json:"pending"`
	Skipped  int    `json:"skipped"`
}

func TestDashboardStats_AggregatesProvidersAndDailySeries(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const reportingTZ = "America/New_York"
	location, err := time.LoadLocation(reportingTZ)
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Now().UTC()
	localDay := now.In(location).AddDate(0, 0, -1)
	crossMidnightUTC := time.Date(localDay.Year(), localDay.Month(), localDay.Day(), 23, 30, 0, 0, location).UTC()
	createdAt := crossMidnightUTC.Format("2006-01-02 15:04:05")

	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET enabled = CASE WHEN job_source = 'LINKEDIN' THEN TRUE ELSE FALSE END,
		    scrape_interval_seconds = 900,
		    last_scraped_at = CASE WHEN job_source = 'LINKEDIN' THEN now() - interval '1 minute' ELSE NULL END,
		    next_eligible_at = NULL
	`); err != nil {
		t.Fatalf("update scraper settings: %v", err)
	}

	insertJob := func(url, state string, reason any) {
		t.Helper()
		if _, err := pool.Exec(`
			INSERT INTO jobs (
				job_source, title, company, location, job_url,
				created_at, updated_at, state, reject_reason, state_changed_at, notified_at
			) VALUES (
				'LINKEDIN', 'IT Help Desk', 'Acme', 'Remote', $1,
				$2::timestamp, $2::timestamp, $3, $4, $5::timestamptz,
				CASE WHEN $3 = 'ready' THEN $5::timestamptz ELSE NULL END
			)
		`, url, createdAt, state, reason, crossMidnightUTC); err != nil {
			t.Fatalf("insert %s: %v", url, err)
		}
	}
	insertJob("https://www.linkedin.com/jobs/view/dashboard-agg-1/", db.JobStatePending, nil)
	insertJob("https://www.linkedin.com/jobs/view/dashboard-agg-2/", db.JobStateRejected, "title_company")
	insertJob("https://www.linkedin.com/jobs/view/dashboard-agg-3/", db.JobStateReady, nil)

	handler := &Handler{
		DB:  pool,
		Cfg: config.Config{ReportingTimezone: reportingTZ},
	}
	body := callDashboardStats(t, handler)

	if body.GeneratedAt == "" {
		t.Fatal("generated_at must be present")
	}
	if _, err := time.Parse(time.RFC3339, body.GeneratedAt); err != nil {
		t.Fatalf("generated_at must be RFC3339: %v", err)
	}
	if body.Timezone != reportingTZ {
		t.Fatalf("timezone = %q, want %q", body.Timezone, reportingTZ)
	}
	if body.Notified != 1 {
		t.Errorf("notified = %d, want 1 from notified_at", body.Notified)
	}
	if len(body.Providers) < 2 {
		t.Fatalf("providers len = %d, want at least 2", len(body.Providers))
	}

	providers := map[string]dashboardProviderStats{}
	for _, provider := range body.Providers {
		providers[provider.JobSource] = provider
	}

	linked, ok := providers["LINKEDIN"]
	if !ok {
		t.Fatal("providers must include LINKEDIN from scraper_settings")
	}
	if !linked.Implemented {
		t.Error("LINKEDIN implemented = false, want true")
	}
	if !linked.Enabled {
		t.Error("LINKEDIN enabled = false, want true")
	}
	if linked.Status != "waiting" {
		t.Errorf("LINKEDIN status = %q, want waiting", linked.Status)
	}
	if linked.TotalJobs != 3 {
		t.Errorf("LINKEDIN total_jobs = %d, want 3", linked.TotalJobs)
	}
	assertStateCounts(t, linked.ByState, map[string]int{
		"pending": 1, "rejected": 1, "needs_detail": 0, "ready": 1, "applied": 0, "dismissed": 0,
	})
	assertReasonCounts(t, linked.ByRejectReason, map[string]int{
		"duplicate": 0, "title_company": 1, "description": 0, "detail_failed": 0, "unsupported_source": 0, "remote_lie": 0,
	})

	indeed, ok := providers["INDEED"]
	if !ok {
		t.Fatal("providers must include INDEED from scraper_settings")
	}
	if !indeed.Implemented {
		t.Error("INDEED implemented = false, want true")
	}

	dice, ok := providers["DICE"]
	if !ok {
		t.Fatal("providers must include DICE from scraper_settings")
	}
	if !dice.Implemented {
		t.Error("DICE implemented = false, want true")
	}

	fantastic, ok := providers["FANTASTIC"]
	if !ok {
		t.Fatal("providers must include FANTASTIC from scraper_settings")
	}
	if !fantastic.Implemented {
		t.Error("FANTASTIC implemented = false, want true")
	}
	if indeed.Enabled {
		t.Error("INDEED enabled = true, want false")
	}
	if indeed.Status != "setup_required" {
		t.Errorf("INDEED status = %q, want setup_required without Apify token", indeed.Status)
	}
	if indeed.TotalJobs != 0 {
		t.Errorf("INDEED total_jobs = %d, want 0", indeed.TotalJobs)
	}
	assertStateCounts(t, indeed.ByState, map[string]int{
		"pending": 0, "rejected": 0, "needs_detail": 0, "ready": 0, "applied": 0, "dismissed": 0,
	})
	assertReasonCounts(t, indeed.ByRejectReason, map[string]int{
		"duplicate": 0, "title_company": 0, "description": 0, "detail_failed": 0, "unsupported_source": 0, "remote_lie": 0,
	})

	expectedDays := make([]string, 0, 14)
	for i := 13; i >= 0; i-- {
		expectedDays = append(expectedDays, now.In(location).AddDate(0, 0, -i).Format("2006-01-02"))
	}
	if len(body.Daily) != len(expectedDays) {
		t.Fatalf("daily len = %d, want %d", len(body.Daily), len(expectedDays))
	}
	dailyTotals := map[string]dashboardDailyPoint{}
	for _, point := range body.Daily {
		dailyTotals[point.Day] = point
	}
	for _, day := range expectedDays {
		if _, ok := dailyTotals[day]; !ok {
			t.Fatalf("daily missing day %s", day)
		}
	}
	shiftedDay := localDay.Format("2006-01-02")
	if got := dailyTotals[shiftedDay].Total; got != 3 {
		t.Errorf("daily total for %s = %d, want 3", shiftedDay, got)
	}
	if got := dailyTotals[shiftedDay].Notified; got != 1 {
		t.Errorf("daily notified for %s = %d, want 1", shiftedDay, got)
	}
	if got := dailyTotals[shiftedDay].Applied; got != 0 {
		t.Errorf("daily applied for %s = %d, want 0", shiftedDay, got)
	}
	if got := dailyTotals[shiftedDay].Pending; got != 2 {
		t.Errorf("daily pending for %s = %d, want 2 (pending + ready)", shiftedDay, got)
	}
	if got := dailyTotals[shiftedDay].Skipped; got != 1 {
		t.Errorf("daily skipped for %s = %d, want 1 (rejected)", shiftedDay, got)
	}
	for _, day := range expectedDays {
		if day == shiftedDay {
			continue
		}
		if got := dailyTotals[day].Total; got != 0 {
			t.Errorf("daily total for %s = %d, want 0", day, got)
		}
		if got := dailyTotals[day].Notified; got != 0 {
			t.Errorf("daily notified for %s = %d, want 0", day, got)
		}
		if got := dailyTotals[day].Applied; got != 0 {
			t.Errorf("daily applied for %s = %d, want 0", day, got)
		}
		if got := dailyTotals[day].Pending; got != 0 {
			t.Errorf("daily pending for %s = %d, want 0", day, got)
		}
		if got := dailyTotals[day].Skipped; got != 0 {
			t.Errorf("daily skipped for %s = %d, want 0", day, got)
		}
	}
}

func TestDashboardStats_DerivedStatusMatchesCadenceRule(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}

	handler := &Handler{
		DB:  pool,
		Cfg: config.Config{ReportingTimezone: "America/New_York"},
	}
	now := time.Now().UTC()

	tests := []struct {
		name      string
		enabled   bool
		last      any
		next      any
		wantState string
	}{
		{
			name:      "disabled",
			enabled:   false,
			last:      nil,
			next:      nil,
			wantState: "disabled",
		},
		{
			name:      "due when never scraped and no backoff",
			enabled:   true,
			last:      nil,
			next:      nil,
			wantState: "due",
		},
		{
			name:      "due when interval elapsed and backoff past",
			enabled:   true,
			last:      now.Add(-20 * time.Minute),
			next:      now.Add(-1 * time.Minute),
			wantState: "due",
		},
		{
			name:      "waiting when interval not elapsed",
			enabled:   true,
			last:      now.Add(-1 * time.Minute),
			next:      nil,
			wantState: "waiting",
		},
		{
			name:      "waiting when backoff is in the future",
			enabled:   true,
			last:      now.Add(-20 * time.Minute),
			next:      now.Add(2 * time.Minute),
			wantState: "waiting",
		},
		{
			name:      "due when interval equals now boundary",
			enabled:   true,
			last:      now.Add(-15 * time.Minute),
			next:      nil,
			wantState: "due",
		},
		{
			name:      "due when backoff equals now boundary",
			enabled:   true,
			last:      now.Add(-20 * time.Minute),
			next:      now,
			wantState: "due",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(`
				UPDATE scraper_settings
				SET enabled = $1,
				    scrape_interval_seconds = 900,
				    last_scraped_at = $2,
				    next_eligible_at = $3
				WHERE job_source = 'LINKEDIN'
			`, tc.enabled, tc.last, tc.next); err != nil {
				t.Fatalf("update cadence: %v", err)
			}

			body := callDashboardStats(t, handler)
			var linked dashboardProviderStats
			found := false
			for _, provider := range body.Providers {
				if provider.JobSource == "LINKEDIN" {
					linked = provider
					found = true
					break
				}
			}
			if !found {
				t.Fatal("LINKEDIN provider not found")
			}
			if linked.Status != tc.wantState {
				t.Errorf("status = %q, want %q", linked.Status, tc.wantState)
			}
		})
	}
}

func TestJobStatsPath_RemainsUnchangedWithDashboardStats(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.InsertJobIfNew(t.Context(), pool, db.Job{
		JobSource: db.SourceLinkedIn,
		Title:     "IT Help Desk",
		Company:   "Acme",
		Location:  "Remote",
		JobURL:    "https://www.linkedin.com/jobs/view/old-job-stats-shape/",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v0/jobs/stats", nil)
	NewServer("", &Handler{DB: pool}).Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/v0/jobs/stats status=%d body=%s", response.Code, response.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("stats JSON: %v", err)
	}
	for _, key := range []string{"total_jobs", "new_jobs", "relevant_jobs", "by_state", "timestamp"} {
		if _, ok := body[key]; !ok {
			t.Errorf("legacy jobs stats missing key %q", key)
		}
	}
	for _, key := range []string{"generated_at", "timezone", "providers", "daily"} {
		if _, ok := body[key]; ok {
			t.Errorf("legacy jobs stats unexpectedly has aggregate key %q", key)
		}
	}
}

func TestDashboardStats_ApifyBudgetTokenMissingNoHTTP(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var apifyCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		apifyCalls.Add(1)
		t.Error("empty token must not call Apify")
	}))
	t.Cleanup(srv.Close)

	fixed := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	handler := budgetTestHandler(pool, srv, "", 100, func() time.Time { return fixed })

	response, body := callDashboardStatsRaw(t, handler)
	if response.Code != http.StatusOK {
		t.Fatalf("dashboard status=%d body=%s", response.Code, response.Body.String())
	}
	if apifyCalls.Load() != 0 {
		t.Fatalf("Apify HTTP calls = %d, want 0", apifyCalls.Load())
	}

	providers := dashboardProvidersRaw(t, body)
	linked, ok := providers["LINKEDIN"]
	if !ok {
		t.Fatal("providers must include LINKEDIN")
	}
	if _, hasBudget := linked["apify_budget"]; hasBudget {
		t.Fatalf("LINKEDIN must omit apify_budget, got %#v", linked["apify_budget"])
	}
	indeedBudget := requireApifyBudget(t, providers["INDEED"])
	assertApifyBudget(t, indeedBudget, budgetExpect{
		reason:  "token_missing",
		blocked: true,
		start:   "2026-09-21T00:00:00Z",
		end:     "2026-10-21T00:00:00Z",
		limit:   "1.00",
	})

	budget := requireApifyBudget(t, providers["DICE"])
	assertApifyBudget(t, budget, budgetExpect{
		reason:  "token_missing",
		blocked: true,
		start:   "2026-09-21T00:00:00Z",
		end:     "2026-10-21T00:00:00Z",
		limit:   "1.00",
	})
	fantasticBudget := requireApifyBudget(t, providers["FANTASTIC"])
	assertApifyBudget(t, fantasticBudget, budgetExpect{
		reason:  "token_missing",
		blocked: true,
		start:   "2026-09-21T00:00:00Z",
		end:     "2026-10-21T00:00:00Z",
		limit:   "1.00",
	})
	indeed := providers["INDEED"]
	if configured, _ := indeed["configured"].(bool); configured {
		t.Fatal("INDEED configured = true with empty APIFY_API_TOKEN, want false")
	}
	if indeed["status"] != "setup_required" {
		t.Fatalf("INDEED status = %v, want setup_required", indeed["status"])
	}
	dice := providers["DICE"]
	if configured, _ := dice["configured"].(bool); configured {
		t.Fatal("DICE configured = true with empty APIFY_API_TOKEN, want false")
	}
	if enabled, _ := dice["enabled"].(bool); enabled {
		t.Fatal("DICE enabled = true with empty APIFY_API_TOKEN, want effective false")
	}
	if dice["status"] != "setup_required" {
		t.Fatalf("DICE status = %v, want setup_required", dice["status"])
	}
	message, _ := dice["configuration_message"].(string)
	if !strings.Contains(message, "Apify account") || !strings.Contains(message, "APIFY_API_TOKEN") {
		t.Fatalf("DICE configuration_message = %q, want Apify setup instructions", message)
	}
}

func TestDashboardStats_ApifyBudgetSuccessAndProcessCache(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var apifyCalls atomic.Int32
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		for {
			old := maxInFlight.Load()
			if cur <= old || maxInFlight.CompareAndSwap(old, cur) {
				break
			}
		}
		defer inFlight.Add(-1)
		apifyCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q, want Bearer test-token", r.Header.Get("Authorization"))
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v2/actor-runs" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		time.Sleep(30 * time.Millisecond)
		writeApifyRuns(w, []map[string]any{
			{"id": "ext-1", "status": "SUCCEEDED", "usageTotalUsd": 0.40, "startedAt": "2026-09-21T01:00:00.000Z"},
			{"id": "ext-2", "status": "SUCCEEDED", "usageTotalUsd": 0.15, "startedAt": "2026-09-21T02:00:00.000Z"},
		})
	}))
	t.Cleanup(srv.Close)

	clock := &budgetClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	handler := budgetTestHandler(pool, srv, "test-token", 100, clock.Now)

	start := make(chan struct{})
	recs := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, dashboardStatsPath, nil)
			NewServer("", handler).Handler.ServeHTTP(rec, req)
			recs[i] = rec
		}(i)
	}
	close(start)
	wg.Wait()
	for i, rec := range recs {
		if rec == nil || rec.Code != http.StatusOK {
			t.Fatalf("concurrent dashboard[%d] missing 200", i)
		}
	}
	if got := apifyCalls.Load(); got != 1 {
		t.Fatalf("concurrent dashboard Apify calls = %d, want 1 coalesced fetch", got)
	}
	if got := maxInFlight.Load(); got != 1 {
		t.Fatalf("concurrent in-flight Apify fetches = %d, want 1", got)
	}

	response, raw := callDashboardStatsRaw(t, handler)
	if response.Code != http.StatusOK {
		t.Fatalf("dashboard status=%d body=%s", response.Code, response.Body.String())
	}
	used, remaining := "0.55", "0.45"
	assertApifyBudget(t, requireApifyBudget(t, dashboardProvidersRaw(t, raw)["DICE"]), budgetExpect{
		reason:    "ok",
		blocked:   false,
		start:     "2026-09-21T00:00:00Z",
		end:       "2026-10-21T00:00:00Z",
		limit:     "1.00",
		used:      &used,
		remaining: &remaining,
	})

	clock.Advance(59 * time.Second)
	_ = callDashboardStats(t, handler)
	if got := apifyCalls.Load(); got != 1 {
		t.Fatalf("Apify calls after 59s = %d, want 1 (60s cache)", got)
	}

	clock.Advance(2 * time.Second)
	_ = callDashboardStats(t, handler)
	if got := apifyCalls.Load(); got != 2 {
		t.Fatalf("Apify calls after 61s = %d, want 2 (cache expired)", got)
	}

	settingsRec := httptest.NewRecorder()
	settingsReq := httptest.NewRequest(http.MethodGet, "/api/v0/scraper-settings?job_source=DICE", nil)
	NewServer("", handler).Handler.ServeHTTP(settingsRec, settingsReq)
	if settingsRec.Code != http.StatusOK {
		t.Fatalf("GET scraper-settings status=%d body=%s", settingsRec.Code, settingsRec.Body.String())
	}
	if got := apifyCalls.Load(); got != 2 {
		t.Fatalf("Apify HTTP calls = %d, want 2 after shared dashboard/settings cache", got)
	}
}

func TestDashboardStats_ApifyBudgetUnavailableKeepsLinkedIn(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := pool.Exec(`
		INSERT INTO jobs (
			job_source, title, company, location, job_url,
			created_at, updated_at, state, state_changed_at
		) VALUES (
			'LINKEDIN', 'IT Help Desk', 'Acme', 'Remote',
			'https://www.linkedin.com/jobs/view/budget-linkedin-1/',
			now(), now(), 'pending', now()
		)
	`); err != nil {
		t.Fatalf("insert LinkedIn job: %v", err)
	}

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	fixed := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	handler := budgetTestHandler(pool, srv, "test-token", 100, func() time.Time { return fixed })
	baseTransport := handler.Scraper.HTTPClient.Transport
	var checkedDeadline atomic.Bool
	handler.Scraper.HTTPClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if checkedDeadline.CompareAndSwap(false, true) {
			deadline, ok := req.Context().Deadline()
			if !ok {
				t.Error("Apify GET must set a deadline")
			} else if remaining := time.Until(deadline); remaining < 1500*time.Millisecond || remaining > 2500*time.Millisecond {
				t.Errorf("Apify GET deadline remaining %s, want ~2s", remaining)
			}
		}
		return baseTransport.RoundTrip(req)
	})

	done := make(chan *httptest.ResponseRecorder, 1)
	started := time.Now()
	go func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, dashboardStatsPath, nil)
		NewServer("", handler).Handler.ServeHTTP(rec, req)
		done <- rec
	}()
	var rec *httptest.ResponseRecorder
	select {
	case rec = <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("dashboard hung waiting for Apify; want 2s GET timeout and HTTP 200")
	}
	if elapsed := time.Since(started); elapsed < 1500*time.Millisecond || elapsed > 3500*time.Millisecond {
		t.Errorf("dashboard took %s, want ~2s Apify GET timeout", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard status=%d body=%s", rec.Code, rec.Body.String())
	}

	providers := dashboardProvidersRaw(t, rec.Body.Bytes())
	linked, ok := providers["LINKEDIN"]
	if !ok {
		t.Fatal("LinkedIn stats must still load when Apify is unavailable")
	}
	total, _ := linked["total_jobs"].(json.Number)
	if total.String() != "1" {
		t.Errorf("LINKEDIN total_jobs = %v, want 1", linked["total_jobs"])
	}
	budget := requireApifyBudget(t, providers["DICE"])
	assertApifyBudget(t, budget, budgetExpect{
		reason:  "apify_unavailable",
		blocked: false,
		start:   "2026-09-21T00:00:00Z",
		end:     "2026-10-21T00:00:00Z",
		limit:   "1.00",
	})
}

func TestDashboardStats_ApifyBudgetExhaustedAndUsageUnknown(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fixed := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

	t.Run("budget_exhausted", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeApifyRuns(w, []map[string]any{
				{"id": "spent", "status": "SUCCEEDED", "usageTotalUsd": 1.00, "startedAt": "2026-09-21T01:00:00.000Z"},
			})
		}))
		t.Cleanup(srv.Close)
		handler := budgetTestHandler(pool, srv, "test-token", 100, func() time.Time { return fixed })
		response, raw := callDashboardStatsRaw(t, handler)
		if response.Code != http.StatusOK {
			t.Fatalf("dashboard status=%d body=%s", response.Code, response.Body.String())
		}
		used, remaining := "1.00", "0.00"
		assertApifyBudget(t, requireApifyBudget(t, dashboardProvidersRaw(t, raw)["DICE"]), budgetExpect{
			reason:    "budget_exhausted",
			blocked:   true,
			start:     "2026-09-21T00:00:00Z",
			end:       "2026-10-21T00:00:00Z",
			limit:     "1.00",
			used:      &used,
			remaining: &remaining,
		})
	})

	t.Run("usage_unknown", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeApifyRuns(w, []map[string]any{
				{"id": "live", "status": "RUNNING", "startedAt": "2026-09-21T03:00:00.000Z"},
			})
		}))
		t.Cleanup(srv.Close)
		handler := budgetTestHandler(pool, srv, "test-token", 100, func() time.Time { return fixed })
		response, raw := callDashboardStatsRaw(t, handler)
		if response.Code != http.StatusOK {
			t.Fatalf("dashboard status=%d body=%s", response.Code, response.Body.String())
		}
		assertApifyBudget(t, requireApifyBudget(t, dashboardProvidersRaw(t, raw)["DICE"]), budgetExpect{
			reason:  "usage_unknown",
			blocked: true,
			start:   "2026-09-21T00:00:00Z",
			end:     "2026-10-21T00:00:00Z",
			limit:   "1.00",
		})
	})
}

type budgetClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *budgetClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *budgetClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func budgetTestHandler(pool *sql.DB, srv *httptest.Server, token string, limitCents int, now func() time.Time) *Handler {
	handler := &Handler{
		DB: pool,
		Cfg: config.Config{
			ReportingTimezone:       "America/New_York",
			ApifyAPIToken:           token,
			ApifyMonthlyBudgetCents: limitCents,
		},
		Scraper: &scraper.Service{
			Now: now,
		},
	}
	if srv != nil {
		handler.Scraper.ApifyBaseURL = srv.URL
		handler.Scraper.HTTPClient = srv.Client()
	}
	return handler
}

func callDashboardStatsRaw(t *testing.T, handler *Handler) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, dashboardStatsPath, nil)
	NewServer("", handler).Handler.ServeHTTP(response, request)
	return response, response.Body.Bytes()
}

func dashboardProvidersRaw(t *testing.T, raw []byte) map[string]map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var body struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("decode dashboard providers: %v", err)
	}
	out := map[string]map[string]any{}
	for _, provider := range body.Providers {
		source, _ := provider["job_source"].(string)
		out[source] = provider
	}
	return out
}

func requireApifyBudget(t *testing.T, provider map[string]any) map[string]any {
	t.Helper()
	if provider == nil {
		t.Fatal("provider is missing")
	}
	raw, ok := provider["apify_budget"]
	if !ok || raw == nil {
		t.Fatal("apify_budget is missing")
	}
	budget, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("apify_budget type %T, want object", raw)
	}
	return budget
}

type budgetExpect struct {
	reason    string
	blocked   bool
	start     string
	end       string
	limit     string
	used      *string
	remaining *string
}

func assertApifyBudget(t *testing.T, budget map[string]any, want budgetExpect) {
	t.Helper()
	assertBudgetReason(t, budget, want.reason)
	blocked, ok := budget["blocked"].(bool)
	if !ok {
		t.Fatalf("blocked type %T, want bool", budget["blocked"])
	}
	if blocked != want.blocked {
		t.Errorf("blocked = %v, want %v", blocked, want.blocked)
	}
	assertBudgetPeriod(t, budget, want.start, want.end)
	assertJSONNumber(t, budget, "limit_usd", want.limit)
	if want.used == nil {
		assertJSONNull(t, budget, "used_usd")
	} else {
		assertJSONNumber(t, budget, "used_usd", *want.used)
	}
	if want.remaining == nil {
		assertJSONNull(t, budget, "remaining_usd")
	} else {
		assertJSONNumber(t, budget, "remaining_usd", *want.remaining)
	}
}

func assertBudgetReason(t *testing.T, budget map[string]any, want string) {
	t.Helper()
	got, _ := budget["reason"].(string)
	if got != want {
		t.Errorf("reason = %q, want %q", got, want)
	}
	switch want {
	case "ok", "token_missing", "budget_exhausted", "usage_unknown", "apify_unavailable":
	default:
		t.Errorf("test want reason %q is not in the allowed set", want)
	}
}

func assertBudgetPeriod(t *testing.T, budget map[string]any, start, end string) {
	t.Helper()
	gotStart, _ := budget["period_start"].(string)
	gotEnd, _ := budget["period_end"].(string)
	if _, err := time.Parse(time.RFC3339, gotStart); err != nil {
		t.Errorf("period_start %q is not RFC3339: %v", gotStart, err)
	}
	if _, err := time.Parse(time.RFC3339, gotEnd); err != nil {
		t.Errorf("period_end %q is not RFC3339: %v", gotEnd, err)
	}
	if gotStart != start || gotEnd != end {
		t.Errorf("period = %s .. %s, want %s .. %s", gotStart, gotEnd, start, end)
	}
}

func assertJSONNumber(t *testing.T, obj map[string]any, key, want string) {
	t.Helper()
	raw, ok := obj[key]
	if !ok {
		t.Fatalf("%s is missing", key)
	}
	num, ok := raw.(json.Number)
	if !ok {
		t.Fatalf("%s type %T, want JSON number", key, raw)
	}
	gotCents, parsed := config.ParseUSDToCents(string(num))
	wantCents, wantOK := config.ParseUSDToCents(want)
	if !parsed || !wantOK || gotCents != wantCents {
		t.Errorf("%s = %q, want %q", key, num, want)
	}
}

func assertJSONNull(t *testing.T, obj map[string]any, key string) {
	t.Helper()
	raw, ok := obj[key]
	if !ok {
		t.Fatalf("%s is missing", key)
	}
	if raw != nil {
		t.Errorf("%s = %#v, want null", key, raw)
	}
}

func writeApifyRuns(w http.ResponseWriter, items []map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]any{
			"total":  len(items),
			"count":  len(items),
			"offset": 0,
			"limit":  1000,
			"items":  items,
		},
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func callDashboardStats(t *testing.T, handler *Handler) dashboardStatsBody {
	t.Helper()
	response, raw := callDashboardStatsRaw(t, handler)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s status=%d body=%s", dashboardStatsPath, response.Code, response.Body.String())
	}
	var body dashboardStatsBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode dashboard stats: %v", err)
	}
	return body
}

func assertStateCounts(t *testing.T, got, want map[string]int) {
	t.Helper()
	for key, count := range want {
		v, ok := got[key]
		if !ok {
			t.Fatalf("by_state missing key %q", key)
		}
		if v != count {
			t.Errorf("by_state[%q] = %d, want %d", key, v, count)
		}
	}
}

func assertReasonCounts(t *testing.T, got, want map[string]int) {
	t.Helper()
	for key, count := range want {
		v, ok := got[key]
		if !ok {
			t.Fatalf("by_reject_reason missing key %q", key)
		}
		if v != count {
			t.Errorf("by_reject_reason[%q] = %d, want %d", key, v, count)
		}
	}
}
