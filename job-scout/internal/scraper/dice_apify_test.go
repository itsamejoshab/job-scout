package scraper

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/db"
)

func TestDiceScrapeJobs_HTTPContract(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	fake.listedRuns = []map[string]any{
		{"id": "prior", "status": "SUCCEEDED", "usageTotalUsd": 0.25, "startedAt": "2026-09-21T01:00:00.000Z"},
	}
	fake.items = []map[string]any{
		{
			"title": "Desktop Support", "company": "Acme", "location": "Daytona Beach, FL",
			"url":              "https://www.dice.com/job-detail/abc?utm=1#frag",
			"description_text": "Support desktops.", "posted": "2026-09-18T15:04:05Z",
			"workSetting": "On-site",
		},
		{
			"title": "Remote Endpoint", "companyName": "Globex",
			"detailsPageUrl":   "https://jobs.dice.com/job-detail/def",
			"description_html": "<div>Help <script>x()</script><p>users</p></div>",
			"posted":           "2026-09-19", "workSetting": "Remote",
		},
		{
			"title": "Application Support", "company": "Redacted Company", "companyName": "Redacted Company",
			"url": "https://www.dice.com/job-detail/live-shape", "detailsPageUrl": "https://www.dice.com/job-detail/live-shape",
			"location": "Example City, FL",
			"jobLocation": map[string]any{
				"city": "Example City", "country": "USA", "region": "FL", "state": "Florida",
			},
			"description_text": "Support applications.", "description_html": "<p>Support applications.</p>",
			"posted": "2026-09-19T15:04:05Z", "postedDate": "2026-09-19T15:04:05Z",
			"workSetting": "Remote", "isRemote": true, "searchIncludeRemote": true,
			"workplaceTypes": []any{"Remote", "On-Site"},
		},
		{"title": "No URL", "company": "Skip Co"},
		{"title": "Bad host", "company": "Skip Co", "url": "https://example.com/job/1"},
		{"company": "No title", "url": "https://www.dice.com/job-detail/no-title"},
	}
	srv := fake.server()
	defer srv.Close()

	scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 2}, newTestApifyClient(srv.URL, "test-token", clock), 100)
	jobs, err := scraper.ScrapeJobs(t.Context(), map[string]string{
		"keywords":       "Desktop Support",
		"location":       "Port Orange, FL",
		"include_remote": "true",
	})
	if err != nil {
		t.Fatalf("ScrapeJobs: %v", err)
	}
	if fake.startCount.Load() != 1 {
		t.Errorf("start POSTs = %d, want 1 (no retry)", fake.startCount.Load())
	}
	if fake.lastStart.auth != "Bearer test-token" {
		t.Errorf("start Authorization = %q, want Bearer test-token", fake.lastStart.auth)
	}
	if !strings.Contains(fake.lastStart.path, "/v2/acts/") || !strings.Contains(fake.lastStart.path, "Dice-Job-Scraper") || !strings.HasSuffix(fake.lastStart.path, "/runs") {
		t.Errorf("start path = %q, want /v2/acts/shahidirfan~Dice-Job-Scraper/runs", fake.lastStart.path)
	}
	for i, auth := range fake.auths() {
		if auth != "Bearer test-token" {
			t.Errorf("request[%d] Authorization = %q, want Bearer test-token", i, auth)
		}
	}
	if fake.lastStart.tokenQuery != "" {
		t.Errorf("start URL token = %q, want empty", fake.lastStart.tokenQuery)
	}
	if fake.lastStart.chargeUSD != "0.75" {
		t.Errorf("maxTotalChargeUsd = %q, want remaining 0.75", fake.lastStart.chargeUSD)
	}
	if fake.lastStart.body["keyword"] != "Desktop Support" {
		t.Errorf("keyword = %#v", fake.lastStart.body["keyword"])
	}
	if fake.lastStart.body["location"] != "Port Orange, FL" {
		t.Errorf("location = %#v", fake.lastStart.body["location"])
	}
	if fake.lastStart.body["posted_date"] != "24h" {
		t.Errorf("posted_date = %#v, want identity 24h", fake.lastStart.body["posted_date"])
	}
	if fake.lastStart.body["includeRemote"] != true {
		t.Errorf("includeRemote = %#v (%T), want bool true", fake.lastStart.body["includeRemote"], fake.lastStart.body["includeRemote"])
	}
	if maxPages, _ := fake.lastStart.body["maxPages"].(float64); maxPages != 2 {
		t.Errorf("maxPages = %#v, want 2", fake.lastStart.body["maxPages"])
	}
	if _, ok := fake.lastStart.body["results_wanted"]; ok {
		t.Errorf("must omit results_wanted, body=%v", fake.lastStart.body)
	}
	if fake.datasetPages < 2 {
		t.Errorf("dataset pages = %d, want paginated fetch until empty/total", fake.datasetPages)
	}
	if len(jobs) != 3 {
		t.Fatalf("mapped jobs = %d, want 3 (skip malformed)", len(jobs))
	}
	if jobs[0].JobURL != "https://www.dice.com/job-detail/abc" {
		t.Errorf("canonical url = %q", jobs[0].JobURL)
	}
	if jobs[0].Company != "Acme" || jobs[0].Title != "Desktop Support" {
		t.Errorf("job 0 title/company = %q / %q", jobs[0].Title, jobs[0].Company)
	}
	if jobs[0].Location != "Daytona Beach, FL" {
		t.Errorf("job 0 location = %q, want item location", jobs[0].Location)
	}
	if jobs[0].IsRemote {
		t.Error("includeRemote search must not copy onto is_remote for onsite workSetting")
	}
	if jobs[0].Source != db.SourceDice {
		t.Errorf("source = %q, want DICE", jobs[0].Source)
	}
	if jobs[1].Company != "Globex" {
		t.Errorf("companyName alias = %q, want Globex", jobs[1].Company)
	}
	if jobs[1].JobURL != "https://jobs.dice.com/job-detail/def" {
		t.Errorf("detailsPageUrl alias = %q", jobs[1].JobURL)
	}
	if jobs[1].Description != "Help users" {
		t.Errorf("html fallback description = %q, want Help users", jobs[1].Description)
	}
	if !jobs[1].IsRemote {
		t.Error("workSetting Remote must set is_remote")
	}
	wantPosted := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	if !jobs[1].Date.Equal(wantPosted) {
		t.Errorf("YYYY-MM-DD posted = %s, want %s", jobs[1].Date, wantPosted)
	}
	if jobs[1].Location != "Port Orange, FL" {
		t.Errorf("empty item location must fall back to search location, got %q", jobs[1].Location)
	}
	if jobs[2].Title != "Application Support" || jobs[2].Company != "Redacted Company" {
		t.Errorf("live-shape title/company = %q / %q", jobs[2].Title, jobs[2].Company)
	}
	if jobs[2].JobURL != "https://www.dice.com/job-detail/live-shape" {
		t.Errorf("live-shape url = %q", jobs[2].JobURL)
	}
	if jobs[2].Location != "Example City, FL" {
		t.Errorf("live-shape location string must win over jobLocation object, got %q", jobs[2].Location)
	}
	if jobs[2].Description != "Support applications." {
		t.Errorf("live-shape description = %q", jobs[2].Description)
	}
	if !jobs[2].IsRemote {
		t.Error("live-shape workSetting Remote must set is_remote")
	}
}

func TestDiceScrapeJobs_DoesNotRetryStart(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	fake.startHTTP = http.StatusInternalServerError
	srv := fake.server()
	defer srv.Close()

	scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 100)
	_, err := scraper.ScrapeJobs(t.Context(), map[string]string{"keywords": "x", "location": "y", "include_remote": "false"})
	if err == nil {
		t.Fatal("start 500 must fail the scrape")
	}
	if fake.startCount.Load() != 1 {
		t.Errorf("start POSTs = %d, want 1 (never retry)", fake.startCount.Load())
	}
}

func TestDiceScrapeJobs_NoStartWhenBudgetOrFailClosed(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}

	t.Run("remaining_zero", func(t *testing.T) {
		fake := newApifyFake(t)
		fake.listedRuns = []map[string]any{{"id": "p", "status": "SUCCEEDED", "usageTotalUsd": 1.00, "startedAt": "2026-09-21T01:00:00.000Z"}}
		srv := fake.server()
		defer srv.Close()
		scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 100)
		_, err := scraper.ScrapeJobs(t.Context(), map[string]string{"keywords": "x", "location": "y", "include_remote": "false"})
		if !IsApifyBudgetBlocked(err) {
			t.Errorf("err = %v, want budget blocked", err)
		}
		if fake.startCount.Load() != 0 {
			t.Errorf("starts = %d, want 0", fake.startCount.Load())
		}
	})

	t.Run("budget_zero", func(t *testing.T) {
		fake := newApifyFake(t)
		srv := fake.server()
		defer srv.Close()
		scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 0)
		_, err := scraper.ScrapeJobs(t.Context(), map[string]string{"keywords": "x", "location": "y", "include_remote": "false"})
		if !IsApifyBudgetBlocked(err) {
			t.Errorf("err = %v, want budget blocked for parsed 0", err)
		}
		if fake.startCount.Load() != 0 {
			t.Errorf("starts = %d, want 0", fake.startCount.Load())
		}
	})

	t.Run("non_terminal_listed", func(t *testing.T) {
		fake := newApifyFake(t)
		fake.listedRuns = []map[string]any{{"id": "live", "status": "RUNNING", "startedAt": "2026-09-21T01:00:00.000Z"}}
		srv := fake.server()
		defer srv.Close()
		scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 100)
		_, err := scraper.ScrapeJobs(t.Context(), map[string]string{"keywords": "x", "location": "y", "include_remote": "false"})
		if !IsApifyFailClosed(err) {
			t.Errorf("err = %v, want fail-closed", err)
		}
		if fake.startCount.Load() != 0 {
			t.Errorf("starts = %d, want 0", fake.startCount.Load())
		}
	})

	t.Run("terminal_usage_unknown", func(t *testing.T) {
		fake := newApifyFake(t)
		fake.listedRuns = []map[string]any{{"id": "done", "status": "SUCCEEDED", "startedAt": "2026-09-21T01:00:00.000Z"}}
		srv := fake.server()
		defer srv.Close()
		scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 100)
		_, err := scraper.ScrapeJobs(t.Context(), map[string]string{"keywords": "x", "location": "y", "include_remote": "false"})
		if !IsApifyFailClosed(err) {
			t.Errorf("err = %v, want fail-closed unknown usage", err)
		}
		if fake.startCount.Load() != 0 {
			t.Errorf("starts = %d, want 0", fake.startCount.Load())
		}
	})
}

func TestDiceScrapeJobs_AbortOnCancelAfterRunIDOnly(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}

	t.Run("after_run_id", func(t *testing.T) {
		fake := newApifyFake(t)
		fake.pollHold = make(chan struct{})
		srv := fake.server()
		defer srv.Close()

		ctx, cancel := context.WithCancel(t.Context())
		scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 100)
		done := make(chan error, 1)
		go func() {
			_, err := scraper.ScrapeJobs(ctx, map[string]string{"keywords": "x", "location": "y", "include_remote": "false"})
			done <- err
		}()
		select {
		case <-fake.pollEntered():
		case <-time.After(2 * time.Second):
			t.Fatal("start did not return a run id")
		}
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("cancelled scrape did not return")
		}
		close(fake.pollHold)
		if fake.abortCount.Load() != 1 {
			t.Errorf("abort POSTs = %d, want 1 after run id", fake.abortCount.Load())
		}
		if fake.lastAbortAuth != "Bearer test-token" {
			t.Errorf("abort Authorization = %q", fake.lastAbortAuth)
		}
	})

	t.Run("before_run_id", func(t *testing.T) {
		fake := newApifyFake(t)
		holdStart := make(chan struct{})
		fake.startHold = holdStart
		srv := fake.server()
		defer srv.Close()

		ctx, cancel := context.WithCancel(t.Context())
		scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 100)
		done := make(chan error, 1)
		go func() {
			_, err := scraper.ScrapeJobs(ctx, map[string]string{"keywords": "x", "location": "y", "include_remote": "false"})
			done <- err
		}()
		select {
		case <-fake.startEntered():
		case <-time.After(2 * time.Second):
			t.Fatal("start request did not begin")
		}
		cancel()
		close(holdStart)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("cancelled scrape did not return")
		}
		if fake.abortCount.Load() != 0 {
			t.Errorf("abort POSTs = %d, want 0 when start never returned a run id", fake.abortCount.Load())
		}
	})
}

func TestDiceScrapeJobs_UsageWaitUsesInjectedSleep(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	start := clock.Now()
	fake.now = clock.Now
	fake.usageAfterTime = start.Add(15 * time.Second)
	fake.items = []map[string]any{{
		"title": "A", "company": "B", "url": "https://www.dice.com/job-detail/wait",
		"description_text": "x",
	}}
	srv := fake.server()
	defer srv.Close()

	wallStart := time.Now()
	scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 100)
	_, err := scraper.ScrapeJobs(t.Context(), map[string]string{"keywords": "x", "location": "y", "include_remote": "false"})
	if err != nil {
		t.Fatalf("ScrapeJobs: %v", err)
	}
	if time.Since(wallStart) > 5*time.Second {
		t.Errorf("usage wait used wall clock; elapsed %s", time.Since(wallStart))
	}
	if clock.Now().Before(start.Add(15 * time.Second)) {
		t.Errorf("injected clock did not advance through 15s usage wait, now=%s", clock.Now())
	}
}

func TestDiceScrapeJobs_UnknownUsageAfterWaitIsFailClosed(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	fake.neverUsage = true
	fake.items = []map[string]any{{
		"title": "A", "company": "B", "url": "https://www.dice.com/job-detail/unknown",
		"description_text": "x",
	}}
	srv := fake.server()
	defer srv.Close()

	scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 100)
	jobs, err := scraper.ScrapeJobs(t.Context(), map[string]string{"keywords": "x", "location": "y", "include_remote": "false"})
	if len(jobs) != 1 {
		t.Errorf("jobs from paid run = %d, want 1 saved by caller", len(jobs))
	}
	if !IsApifyFailClosed(err) {
		t.Errorf("err = %v, want fail-closed unknown usage after 15s", err)
	}
}

func TestMapDiceItem_AliasesCanonicalURLRemoteAndDates(t *testing.T) {
	scraped := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	t.Run("skip_unusable", func(t *testing.T) {
		cases := []map[string]any{
			{"title": "A", "company": "B"},
			{"title": "A", "url": "https://www.dice.com/job-detail/x"},
			{"company": "B", "url": "https://www.dice.com/job-detail/x"},
			{"title": "A", "company": "B", "url": "ftp://dice.com/job"},
			{"title": "A", "company": "B", "url": "https://notdice.com/job"},
		}
		for i, item := range cases {
			if _, ok := MapDiceItem(item, "Port Orange, FL", scraped); ok {
				t.Errorf("case %d mapped, want skip: %#v", i, item)
			}
		}
	})
	t.Run("remote_from_location_text_not_includeRemote", func(t *testing.T) {
		job, ok := MapDiceItem(map[string]any{
			"title": "A", "company": "B",
			"url":      "https://www.dice.com/job-detail/rem",
			"location": "Remote, United States",
		}, "Port Orange, FL", scraped)
		if !ok {
			t.Fatal("wanted map")
		}
		if !job.IsRemote {
			t.Error("location text containing remote must set is_remote")
		}
	})
	t.Run("rfc3339_posted", func(t *testing.T) {
		job, ok := MapDiceItem(map[string]any{
			"title": "A", "company": "B",
			"url":    "https://www.dice.com/job-detail/d",
			"posted": "2026-09-18T15:04:05Z",
		}, "FL", scraped)
		if !ok {
			t.Fatal("wanted map")
		}
		want := time.Date(2026, 9, 18, 15, 4, 5, 0, time.UTC)
		if !job.Date.Equal(want) {
			t.Errorf("date = %s, want %s", job.Date, want)
		}
	})
	t.Run("bad_posted_uses_scrape_time", func(t *testing.T) {
		job, ok := MapDiceItem(map[string]any{
			"title": "A", "company": "B",
			"url":    "https://www.dice.com/job-detail/e",
			"posted": "whenever",
		}, "FL", scraped)
		if !ok {
			t.Fatal("wanted map")
		}
		if !job.Date.Equal(scraped) {
			t.Errorf("date = %s, want scrape time %s", job.Date, scraped)
		}
	})
	t.Run("jobLocation_string_and_text_over_html", func(t *testing.T) {
		job, ok := MapDiceItem(map[string]any{
			"title": "A", "company": "B",
			"url":              "https://www.dice.com/job-detail/loc",
			"jobLocation":      "Orlando, FL",
			"description_text": "plain text",
			"description_html": "<p>html only</p>",
		}, "Port Orange, FL", scraped)
		if !ok {
			t.Fatal("wanted map")
		}
		if job.Location != "Orlando, FL" {
			t.Errorf("jobLocation = %q, want Orlando, FL", job.Location)
		}
		if job.Description != "plain text" {
			t.Errorf("description_text must win, got %q", job.Description)
		}
		if job.Description == "" {
			t.Error("mapped empty description is allowed later; this fixture must keep text")
		}
	})
	t.Run("empty_description_still_maps", func(t *testing.T) {
		job, ok := MapDiceItem(map[string]any{
			"title": "A", "company": "B",
			"url": "https://www.dice.com/job-detail/empty-desc",
		}, "FL", scraped)
		if !ok {
			t.Fatal("wanted map")
		}
		if job.Description != "" {
			t.Errorf("description = %q, want empty", job.Description)
		}
	})
	t.Run("remote_case_insensitive", func(t *testing.T) {
		job, ok := MapDiceItem(map[string]any{
			"title": "A", "company": "B",
			"url":         "https://www.dice.com/job-detail/case",
			"workSetting": "REMOTE hybrid",
		}, "FL", scraped)
		if !ok {
			t.Fatal("wanted map")
		}
		if !job.IsRemote {
			t.Error("workSetting REMOTE must set is_remote")
		}
	})
	t.Run("live_object_jobLocation_uses_location_string", func(t *testing.T) {
		job, ok := MapDiceItem(map[string]any{
			"title": "Application Support", "company": "Redacted Company", "companyName": "Redacted Company",
			"url": "https://www.dice.com/job-detail/live-obj", "detailsPageUrl": "https://www.dice.com/job-detail/live-obj",
			"location": "Example City, FL",
			"jobLocation": map[string]any{
				"city": "Other City", "country": "USA", "region": "CA", "state": "California",
			},
			"description_text": "Support applications.", "description_html": "<p>html</p>",
			"posted": "2026-09-19T15:04:05Z", "postedDate": "2026-09-18T00:00:00Z",
			"workSetting": "Remote",
		}, "Port Orange, FL", scraped)
		if !ok {
			t.Fatal("wanted map")
		}
		if job.Location != "Example City, FL" {
			t.Errorf("location = %q, want string location over object jobLocation", job.Location)
		}
		want := time.Date(2026, 9, 19, 15, 4, 5, 0, time.UTC)
		if !job.Date.Equal(want) {
			t.Errorf("date = %s, want posted not postedDate alias %s", job.Date, want)
		}
	})
	t.Run("live_missing_location_falls_back_and_maps", func(t *testing.T) {
		job, ok := MapDiceItem(map[string]any{
			"title": "Desktop Support", "company": "Redacted Company",
			"url": "https://www.dice.com/job-detail/live-noloc",
			"description_text": "Support desktops.",
			"posted":           "2026-09-19T15:04:05Z",
			"workSetting":      "Remote",
		}, "Port Orange, FL", scraped)
		if !ok {
			t.Fatal("wanted map")
		}
		if job.Location != "Port Orange, FL" {
			t.Errorf("location = %q, want search fallback", job.Location)
		}
		if !job.IsRemote {
			t.Error("workSetting Remote must set is_remote when location is missing")
		}
	})
	t.Run("live_actor_isRemote_does_not_override_onsite", func(t *testing.T) {
		job, ok := MapDiceItem(map[string]any{
			"title": "A", "company": "B",
			"url":                 "https://www.dice.com/job-detail/live-onsite",
			"location":            "Example City, FL",
			"workSetting":         "On-site",
			"isRemote":            true,
			"searchIncludeRemote": true,
		}, "Port Orange, FL", scraped)
		if !ok {
			t.Fatal("wanted map")
		}
		if job.IsRemote {
			t.Error("actor isRemote and searchIncludeRemote must not set is_remote for onsite workSetting")
		}
	})
}

type apifyFake struct {
	t              *testing.T
	listedRuns     []map[string]any
	items          []map[string]any
	startHTTP      int
	startHold      chan struct{}
	pollHold       chan struct{}
	onStart        func()
	now            func() time.Time
	usageAfterTime time.Time
	neverUsage     bool
	pollStatus     []string
	listFailLeft   atomic.Int32
	getFailLeft    atomic.Int32

	startCount    atomic.Int32
	abortCount    atomic.Int32
	datasetPages  int
	lastStart     apifyStart
	lastAbortAuth string
	listDesc      string
	listLimit     string
	enteredStart  chan struct{}
	enteredPoll   chan struct{}
	authMu        sync.Mutex
	authHeaders   []string
	pollIdx       atomic.Int32
	mu            sync.Mutex
}

type apifyStart struct {
	auth       string
	tokenQuery string
	chargeUSD  string
	body       map[string]any
	path       string
}

func newApifyFake(t *testing.T) *apifyFake {
	t.Helper()
	return &apifyFake{t: t, startHTTP: http.StatusCreated, items: []map[string]any{}, enteredStart: make(chan struct{}, 1), enteredPoll: make(chan struct{}, 4)}
}

func (f *apifyFake) auths() []string {
	f.authMu.Lock()
	defer f.authMu.Unlock()
	out := make([]string, len(f.authHeaders))
	copy(out, f.authHeaders)
	return out
}

func (f *apifyFake) pollEntered() <-chan struct{} {
	return f.enteredPoll
}

func (f *apifyFake) startEntered() <-chan struct{} {
	return f.enteredStart
}

func (f *apifyFake) recordAuth(auth string) {
	f.authMu.Lock()
	f.authHeaders = append(f.authHeaders, auth)
	f.authMu.Unlock()
}

func (f *apifyFake) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		f.recordAuth(auth)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/actor-runs":
			f.listDesc = r.URL.Query().Get("desc")
			f.listLimit = r.URL.Query().Get("limit")
			if f.listFailLeft.Add(-1)+1 > 0 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{"data": map[string]any{
				"total": len(f.listedRuns), "count": len(f.listedRuns), "items": f.listedRuns,
			}})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "Dice-Job-Scraper") && strings.HasSuffix(r.URL.Path, "/runs"):
			f.startCount.Add(1)
			select {
			case f.enteredStart <- struct{}{}:
			default:
			}
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			f.mu.Lock()
			f.lastStart = apifyStart{auth: auth, tokenQuery: r.URL.Query().Get("token"), chargeUSD: r.URL.Query().Get("maxTotalChargeUsd"), body: body, path: r.URL.Path}
			f.mu.Unlock()
			if f.startHold != nil {
				<-f.startHold
			}
			if f.startHTTP != http.StatusCreated && f.startHTTP != http.StatusOK {
				w.WriteHeader(f.startHTTP)
				return
			}
			writeJSON(w, map[string]any{"data": map[string]any{
				"id": "run-1", "status": "RUNNING", "defaultDatasetId": "ds-1",
			}})
			if f.onStart != nil {
				f.onStart()
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v2/actor-runs/run-1":
			select {
			case f.enteredPoll <- struct{}{}:
			default:
			}
			if f.getFailLeft.Add(-1)+1 > 0 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if f.pollHold != nil {
				select {
				case <-f.pollHold:
				case <-r.Context().Done():
					return
				}
			}
			status := "SUCCEEDED"
			if len(f.pollStatus) > 0 {
				n := int(f.pollIdx.Add(1))
				if n > len(f.pollStatus) {
					status = f.pollStatus[len(f.pollStatus)-1]
				} else {
					status = f.pollStatus[n-1]
				}
			}
			data := map[string]any{"id": "run-1", "status": status, "defaultDatasetId": "ds-1"}
			includeUsage := !f.neverUsage && status == "SUCCEEDED"
			if includeUsage && !f.usageAfterTime.IsZero() {
				includeUsage = f.now != nil && !f.now().Before(f.usageAfterTime)
			}
			if includeUsage {
				data["usageTotalUsd"] = 0.01
			}
			writeJSON(w, map[string]any{"data": data})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v2/datasets/ds-1/items"):
			f.mu.Lock()
			f.datasetPages++
			page := f.datasetPages
			f.mu.Unlock()
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			if limit != 1000 {
				f.t.Errorf("dataset limit = %d, want 1000", limit)
			}
			total := len(f.items)
			end := offset + limit
			if end > total {
				end = total
			}
			var pageItems []map[string]any
			if offset < total {
				pageItems = f.items[offset:end]
			}
			if page == 1 && total > 1 {
				pageItems = f.items[:1]
				w.Header().Set("X-Apify-Pagination-Total", strconv.Itoa(total))
				writeJSON(w, pageItems)
				return
			}
			w.Header().Set("X-Apify-Pagination-Total", strconv.Itoa(total))
			if pageItems == nil {
				pageItems = []map[string]any{}
			}
			writeJSON(w, pageItems)
		case r.Method == http.MethodPost && r.URL.Path == "/v2/actor-runs/run-1/abort":
			f.abortCount.Add(1)
			f.lastAbortAuth = auth
			writeJSON(w, map[string]any{"data": map[string]any{"id": "run-1", "status": "ABORTING"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return nil
}

func newTestApifyClient(baseURL, token string, clock *manualClock) *ApifyClient {
	return &ApifyClient{
		BaseURL: baseURL,
		Token:   token,
		HTTP:    &http.Client{Timeout: 5 * time.Second},
		Now:     clock.Now,
		Sleep:   clock.Sleep,
	}
}
