package scraper

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/db"
)

func TestRunTick_EmptyApifyTokenSkipsDiceWithoutErrorBackoff(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	if _, err := pool.Exec(`
		UPDATE scraper_settings SET enabled = false WHERE job_source IN ('LINKEDIN', 'INDEED');
		UPDATE scraper_settings SET enabled = true, last_scraped_at = NULL, next_eligible_at = NULL,
		       global_searches = '["stale global"]'::json
		WHERE job_source = 'DICE'
	`); err != nil {
		t.Fatalf("enable only Dice: %v", err)
	}
	var nextBefore *time.Time
	dice, err := db.GetScraperSettings(ctx, pool, db.SourceDice)
	if err != nil || dice == nil {
		t.Fatalf("load Dice settings: %v", err)
	}
	nextBefore = dice.NextEligibleAt

	s := NewService(pool)
	s.ApifyToken = ""
	s.ApifyBudgetCents = 100
	result, err := s.RunTick(ctx, TickInput{Force: true, JobSource: "DICE"})
	if err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	if result.Status == "success" {
		t.Error("missing token must not look like a successful scrape")
	}
	if !IsApifyTokenMissing(errResultError(result)) && !strings.Contains(strings.ToLower(result.Error), "token") {
		t.Errorf("missing token error = %q, want a clear token error", result.Error)
	}

	after, err := db.GetScraperSettings(ctx, pool, db.SourceDice)
	if err != nil {
		t.Fatalf("reload Dice: %v", err)
	}
	if after.LastScrapedAt != nil {
		t.Error("missing token must not set last_scraped_at")
	}
	if (after.NextEligibleAt == nil) != (nextBefore == nil) {
		t.Error("missing token must not apply scrape error backoff")
	}
	if after.NextEligibleAt != nil && nextBefore != nil && !after.NextEligibleAt.Equal(*nextBefore) {
		t.Error("missing token must not change next_eligible_at")
	}
}

func TestRunFullScrape_DiceBudgetBlockSetsNextPeriodAndKeepsEnabled(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	if _, err := pool.Exec(`
		UPDATE scraper_settings SET enabled = true, last_scraped_at = NULL, next_eligible_at = NULL,
		       global_searches = '["stale global"]'::json
		WHERE job_source = 'DICE'
	`); err != nil {
		t.Fatalf("enable Dice: %v", err)
	}

	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	fake.listedRuns = []map[string]any{{
		"id": "spent", "status": "SUCCEEDED", "usageTotalUsd": 1.00, "startedAt": "2026-09-21T01:00:00.000Z",
	}}
	srv := fake.server()
	defer srv.Close()

	s := NewService(pool)
	s.ApifyToken = "test-token"
	s.ApifyBudgetCents = 100
	s.ApifyBaseURL = srv.URL
	s.Now = clock.Now
	s.Sleep = clock.Sleep
	s.ErrorBackoff = 5 * time.Minute
	result, err := s.RunFullScrape(ctx, db.SourceDice)
	if err != nil {
		t.Fatalf("RunFullScrape: %v", err)
	}
	if fake.startCount.Load() != 0 {
		t.Errorf("budget block starts = %d, want 0", fake.startCount.Load())
	}
	if result.Status == "success" {
		t.Error("budget block must not succeed")
	}

	stored, err := db.GetScraperSettings(ctx, pool, db.SourceDice)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !stored.Enabled {
		t.Error("budget block must leave enabled true")
	}
	if stored.LastScrapedAt != nil {
		t.Error("budget block must not set last_scraped_at")
	}
	_, periodEnd := ApifyBudgetPeriod(clock.Now())
	if stored.NextEligibleAt == nil || !stored.NextEligibleAt.Equal(periodEnd) {
		t.Errorf("next_eligible_at = %v, want period end %s", stored.NextEligibleAt, periodEnd)
	}
}

func TestRunFullScrape_DiceGlobalsNotExecutedAndFailClosedStopsLaterPairs(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	queries, err := json.Marshal([]map[string]string{
		{"keywords": "First", "location": "Port Orange, FL", "include_remote": "true"},
		{"keywords": "Second", "location": "Port Orange, FL", "include_remote": "false"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET enabled = true, search_queries = $1::json, global_searches = '["must not run"]'::json,
		    last_scraped_at = NULL, next_eligible_at = NULL, rounds = 1, pages_to_scrape = 1
		WHERE job_source = 'DICE'
	`, queries); err != nil {
		t.Fatalf("configure Dice: %v", err)
	}

	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	fake.neverUsage = true
	fake.items = []map[string]any{{
		"title": "Help Desk", "company": "Acme",
		"url": "https://www.dice.com/job-detail/first-pair", "description_text": "Support computers.",
	}}
	srv := fake.server()
	defer srv.Close()

	s := NewService(pool)
	s.ApifyToken = "test-token"
	s.ApifyBudgetCents = 100
	s.ApifyBaseURL = srv.URL
	s.Now = clock.Now
	s.Sleep = clock.Sleep
	result, err := s.RunFullScrape(ctx, db.SourceDice)
	if err != nil {
		t.Fatalf("RunFullScrape: %v", err)
	}
	if fake.startCount.Load() != 1 {
		t.Errorf("starts = %d, want 1 (stop after fail-closed usage wait; no globals)", fake.startCount.Load())
	}
	if result.Status != "error" {
		t.Errorf("mixed scrape status = %q, want error for cadence", result.Status)
	}
	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM jobs WHERE job_source = 'DICE'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("jobs saved from first pair = %d, want 1", n)
	}
	var ctxText string
	if err := pool.QueryRow(`SELECT search_context FROM jobs WHERE job_source = 'DICE'`).Scan(&ctxText); err != nil {
		t.Fatalf("search_context: %v", err)
	}
	if strings.Contains(ctxText, "must not run") || strings.Contains(ctxText, "global") {
		t.Errorf("Dice globals must not run, search_context=%q", ctxText)
	}
	if !strings.Contains(ctxText, `include_remote="true"`) {
		t.Errorf("search_context = %q, want include_remote", ctxText)
	}
}

func TestRunFullScrape_DiceSkipsStartsWhenActivityTimeLow(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	queries, err := json.Marshal([]map[string]string{
		{"keywords": "First", "location": "Port Orange, FL", "include_remote": "true"},
		{"keywords": "Second", "location": "Daytona Beach, FL", "include_remote": "true"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET enabled = true, search_queries = $1::json, global_searches = '[]'::json,
		    last_scraped_at = NULL, next_eligible_at = NULL
		WHERE job_source = 'DICE'
	`, queries); err != nil {
		t.Fatalf("configure Dice: %v", err)
	}

	deadline := time.Now().Add(5 * time.Minute)
	short, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	fake.items = []map[string]any{{
		"title": "Help Desk", "company": "Acme",
		"url": "https://www.dice.com/job-detail/time-skip", "description_text": "x",
	}}
	srv := fake.server()
	defer srv.Close()

	s := NewService(pool)
	s.ApifyToken = "test-token"
	s.ApifyBudgetCents = 100
	s.ApifyBaseURL = srv.URL
	s.Now = clock.Now
	s.Sleep = clock.Sleep
	result, err := s.RunFullScrape(short, db.SourceDice)
	if err != nil {
		t.Fatalf("RunFullScrape: %v", err)
	}
	if fake.startCount.Load() != 0 {
		t.Errorf("starts = %d, want 0 when remaining activity time < 13m", fake.startCount.Load())
	}
	if result.SkippedCount != 2 {
		t.Errorf("skipped_count = %d, want 2 pairs", result.SkippedCount)
	}
}

func TestRunFullScrape_DiceTakesApifyLockAroundStart(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	if _, err := pool.Exec(`
		UPDATE scraper_settings SET enabled = true, last_scraped_at = NULL, next_eligible_at = NULL
		WHERE job_source = 'DICE'
	`); err != nil {
		t.Fatalf("enable Dice: %v", err)
	}

	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	var locked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext('APIFY'))`).Scan(&locked); err != nil {
		t.Fatalf("hold APIFY: %v", err)
	}
	if !locked {
		t.Fatal("test must hold APIFY lock")
	}
	defer func() {
		_, _ = conn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtext('APIFY'))`)
	}()

	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	srv := fake.server()
	defer srv.Close()

	s := NewService(pool)
	s.ApifyToken = "test-token"
	s.ApifyBudgetCents = 100
	s.ApifyBaseURL = srv.URL
	s.Now = clock.Now
	waits := 0
	s.Sleep = func(ctx context.Context, d time.Duration) error {
		waits++
		if waits >= 3 {
			return context.DeadlineExceeded
		}
		return nil
	}
	result, err := s.RunFullScrape(ctx, db.SourceDice)
	if err != nil {
		t.Fatalf("RunFullScrape: %v", err)
	}
	if strings.Contains(result.Error, "no scraper available") {
		t.Errorf("Dice must be wired; error=%q", result.Error)
	}
	if fake.startCount.Load() != 0 {
		t.Errorf("starts = %d, want 0 while APIFY lock is held", fake.startCount.Load())
	}
	if waits < 1 {
		t.Errorf("must wait/retry for APIFY lock, waits=%d", waits)
	}
	if result.Status != "skipped" {
		t.Errorf("status = %q, want skipped when APIFY lock wait times out", result.Status)
	}
	if !IsApifyFailClosed(errResultFrom(result)) && !strings.Contains(strings.ToLower(result.Error), "lock") {
		t.Errorf("error = %q, want APIFY lock busy", result.Error)
	}
}

func errResultError(r Result) error {
	if r.Error == "" {
		return nil
	}
	if strings.Contains(strings.ToLower(r.Error), "token") {
		return ErrApifyTokenMissing
	}
	return nil
}

func errResultFrom(r Result) error {
	if r.Error == "" {
		return nil
	}
	if strings.Contains(strings.ToLower(r.Error), "lock") {
		return ErrApifyLockBusy
	}
	if strings.Contains(strings.ToLower(r.Error), "token") {
		return ErrApifyTokenMissing
	}
	return nil
}
