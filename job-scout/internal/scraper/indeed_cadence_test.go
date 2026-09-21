package scraper

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/db"
)

func TestRunTick_EmptyApifyTokenSkipsIndeedWithoutErrorBackoff(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	if _, err := pool.Exec(`
		UPDATE scraper_settings SET enabled = false WHERE job_source IN ('LINKEDIN', 'DICE');
		UPDATE scraper_settings SET enabled = true, last_scraped_at = NULL, next_eligible_at = NULL
		WHERE job_source = 'INDEED'
	`); err != nil {
		t.Fatalf("enable only Indeed: %v", err)
	}

	s := NewService(pool)
	s.ApifyToken = ""
	s.ApifyBudgetCents = 100
	result, err := s.RunTick(ctx, TickInput{Force: true, JobSource: "INDEED"})
	if err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	if result.Status == "success" {
		t.Error("missing token must not look like a successful scrape")
	}
	if !strings.Contains(strings.ToLower(result.Error), "apify") && !strings.Contains(strings.ToLower(result.Error), "token") {
		t.Errorf("missing token error = %q, want Apify/token message", result.Error)
	}

	after, err := db.GetScraperSettings(ctx, pool, db.SourceIndeed)
	if err != nil {
		t.Fatalf("reload Indeed: %v", err)
	}
	if after.LastScrapedAt != nil {
		t.Error("missing token must not set last_scraped_at")
	}
}

func TestRunFullScrape_IndeedSuccessUpdatesCadence(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	queries, err := json.Marshal([]map[string]string{{
		"keywords": "Desktop", "location": "Port Orange, FL",
		"include_remote": "false", "include_hybrid": "false",
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	opts, err := json.Marshal(db.IndeedOptionsMap(db.DefaultIndeedOptions()))
	if err != nil {
		t.Fatalf("marshal options: %v", err)
	}
	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET enabled = true, last_scraped_at = NULL, next_eligible_at = NULL,
		    search_queries = $1::json, global_searches = '[]'::json,
		    provider_options = $2::json, timespan_code = '1'
		WHERE job_source = 'INDEED'
	`, queries, opts); err != nil {
		t.Fatalf("configure Indeed: %v", err)
	}

	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	fake.items = []map[string]any{{
		"title": "Desktop Support", "companyName": "Acme",
		"jobUrl":          "https://www.indeed.com/viewjob?jk=cadence-ok",
		"descriptionText": "Support desktops and endpoints.",
		"datePublished":   "2026-09-21",
	}}
	srv := fake.server()
	defer srv.Close()

	s := NewService(pool)
	s.ApifyToken = "test-token"
	s.ApifyBudgetCents = 100
	s.ApifyBaseURL = srv.URL
	s.Now = clock.Now
	s.Sleep = clock.Sleep
	result, err := s.RunFullScrape(ctx, db.SourceIndeed)
	if err != nil {
		t.Fatalf("RunFullScrape: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("status = %q error=%q, want success", result.Status, result.Error)
	}
	if result.SavedCount != 1 {
		t.Errorf("saved = %d, want 1", result.SavedCount)
	}
	if !strings.Contains(fake.lastStart.path, "indeed-scraper") {
		t.Errorf("actor path = %q", fake.lastStart.path)
	}

	stored, err := db.GetScraperSettings(ctx, pool, db.SourceIndeed)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.LastScrapedAt == nil || !stored.LastScrapedAt.Equal(clock.Now()) {
		t.Errorf("last_scraped_at = %v, want %s", stored.LastScrapedAt, clock.Now())
	}

	var count int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM jobs WHERE job_source = 'INDEED'`).Scan(&count); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 1 {
		t.Errorf("Indeed jobs = %d, want 1", count)
	}
}
