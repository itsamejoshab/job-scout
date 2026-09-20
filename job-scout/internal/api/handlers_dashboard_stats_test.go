package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
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
	JobSource             string         `json:"job_source"`
	Implemented           bool           `json:"implemented"`
	Enabled               bool           `json:"enabled"`
	ScrapeIntervalSeconds int            `json:"scrape_interval_seconds"`
	LastScrapedAt         *string        `json:"last_scraped_at"`
	NextEligibleAt        *string        `json:"next_eligible_at"`
	Status                string         `json:"status"`
	TotalJobs             int            `json:"total_jobs"`
	ByState               map[string]int `json:"by_state"`
	ByRejectReason        map[string]int `json:"by_reject_reason"`
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
		"duplicate": 0, "title_company": 1, "description": 0, "detail_failed": 0, "unsupported_source": 0,
	})

	indeed, ok := providers["INDEED"]
	if !ok {
		t.Fatal("providers must include INDEED from scraper_settings")
	}
	if indeed.Implemented {
		t.Error("INDEED implemented = true, want false")
	}
	if indeed.Enabled {
		t.Error("INDEED enabled = true, want false")
	}
	if indeed.Status != "disabled" {
		t.Errorf("INDEED status = %q, want disabled", indeed.Status)
	}
	if indeed.TotalJobs != 0 {
		t.Errorf("INDEED total_jobs = %d, want 0", indeed.TotalJobs)
	}
	assertStateCounts(t, indeed.ByState, map[string]int{
		"pending": 0, "rejected": 0, "needs_detail": 0, "ready": 0, "applied": 0, "dismissed": 0,
	})
	assertReasonCounts(t, indeed.ByRejectReason, map[string]int{
		"duplicate": 0, "title_company": 0, "description": 0, "detail_failed": 0, "unsupported_source": 0,
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

func callDashboardStats(t *testing.T, handler *Handler) dashboardStatsBody {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, dashboardStatsPath, nil)
	NewServer("", handler).Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s status=%d body=%s", dashboardStatsPath, response.Code, response.Body.String())
	}
	var body dashboardStatsBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
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
