package scraper

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/db"
)

func TestDiceScrapeJobs_IncludeRemoteFalseAndPostedDateIdentity(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	srv := fake.server()
	defer srv.Close()
	scraper := NewDice(db.ScraperSettings{TimespanCode: "7d", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 100)
	_, err := scraper.ScrapeJobs(t.Context(), map[string]string{
		"keywords": "Endpoint", "location": "FL", "include_remote": "false",
	})
	if err != nil {
		t.Fatalf("ScrapeJobs: %v", err)
	}
	if fake.lastStart.body["posted_date"] != "7d" {
		t.Errorf("posted_date = %#v, want 7d", fake.lastStart.body["posted_date"])
	}
	if fake.lastStart.body["includeRemote"] != false {
		t.Errorf("includeRemote = %#v (%T), want bool false", fake.lastStart.body["includeRemote"], fake.lastStart.body["includeRemote"])
	}
}

func TestDiceScrapeJobs_GETRetriesThenSucceeds(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	fake.listFailLeft.Store(3)
	srv := fake.server()
	defer srv.Close()
	scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 100)
	_, err := scraper.ScrapeJobs(t.Context(), map[string]string{"keywords": "x", "location": "y", "include_remote": "false"})
	if err != nil {
		t.Fatalf("3 GET failures must be retried, err=%v", err)
	}
	if fake.startCount.Load() != 1 {
		t.Errorf("starts = %d, want 1 after GET retries", fake.startCount.Load())
	}
	if clock.Now().Before(time.Date(2026, 9, 22, 12, 0, 3, 0, time.UTC)) {
		t.Errorf("GET retries must use injected backoff, now=%s", clock.Now())
	}
}

func TestDiceScrapeJobs_GETRetriesExhausted(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	fake.listFailLeft.Store(4)
	srv := fake.server()
	defer srv.Close()
	scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 100)
	_, err := scraper.ScrapeJobs(t.Context(), map[string]string{"keywords": "x", "location": "y", "include_remote": "false"})
	if err == nil {
		t.Fatal("4 GET failures must fail the scrape")
	}
	if fake.startCount.Load() != 0 {
		t.Errorf("starts = %d, want 0 when list GET retries exhaust", fake.startCount.Load())
	}
}

func TestDiceScrapeJobs_PollIntervalAndTimeout(t *testing.T) {
	t.Run("sleeps_two_seconds_while_running", func(t *testing.T) {
		clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
		fake := newApifyFake(t)
		fake.pollStatus = []string{"RUNNING", "SUCCEEDED"}
		srv := fake.server()
		defer srv.Close()
		scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 100)
		_, err := scraper.ScrapeJobs(t.Context(), map[string]string{"keywords": "x", "location": "y", "include_remote": "false"})
		if err != nil {
			t.Fatalf("ScrapeJobs: %v", err)
		}
		if clock.Now().Before(time.Date(2026, 9, 22, 12, 0, 2, 0, time.UTC)) {
			t.Errorf("poll must wait 2s of injected time, now=%s", clock.Now())
		}
	})
	t.Run("stops_after_twelve_minutes", func(t *testing.T) {
		clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
		fake := newApifyFake(t)
		fake.pollStatus = []string{"RUNNING"}
		srv := fake.server()
		defer srv.Close()
		scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 100)
		_, err := scraper.ScrapeJobs(t.Context(), map[string]string{"keywords": "x", "location": "y", "include_remote": "false"})
		if err == nil {
			t.Fatal("RUNNING past 12m must fail")
		}
		if clock.Now().Before(time.Date(2026, 9, 22, 12, 12, 0, 0, time.UTC)) {
			t.Errorf("poll timeout must consume 12m injected time, now=%s", clock.Now())
		}
	})
	t.Run("unknown_status_is_failure", func(t *testing.T) {
		clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
		fake := newApifyFake(t)
		fake.pollStatus = []string{"NOPE"}
		srv := fake.server()
		defer srv.Close()
		scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 100)
		_, err := scraper.ScrapeJobs(t.Context(), map[string]string{"keywords": "x", "location": "y", "include_remote": "false"})
		if err == nil {
			t.Fatal("unknown poll status must fail")
		}
	})
}

func TestDiceScrapeJobs_OverspendAndNegativeBudget(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	t.Run("overspend", func(t *testing.T) {
		fake := newApifyFake(t)
		fake.listedRuns = []map[string]any{{"id": "p", "status": "SUCCEEDED", "usageTotalUsd": 1.01, "startedAt": "2026-09-21T01:00:00.000Z"}}
		srv := fake.server()
		defer srv.Close()
		scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 100)
		_, err := scraper.ScrapeJobs(t.Context(), map[string]string{"keywords": "x", "location": "y", "include_remote": "false"})
		if !IsApifyBudgetBlocked(err) {
			t.Errorf("err = %v, want budget blocked on remaining < 0", err)
		}
		if fake.startCount.Load() != 0 {
			t.Errorf("starts = %d, want 0", fake.startCount.Load())
		}
	})
	t.Run("negative_budget", func(t *testing.T) {
		fake := newApifyFake(t)
		srv := fake.server()
		defer srv.Close()
		scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), -1)
		_, err := scraper.ScrapeJobs(t.Context(), map[string]string{"keywords": "x", "location": "y", "include_remote": "false"})
		if !IsApifyBudgetBlocked(err) {
			t.Errorf("err = %v, want budget blocked", err)
		}
		if fake.startCount.Load() != 0 {
			t.Errorf("starts = %d, want 0", fake.startCount.Load())
		}
	})
}

func TestApifyClient_AccountBudget_ListQueryAndPeriodEndExclusive(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	fake.listedRuns = []map[string]any{
		{"id": "in", "status": "SUCCEEDED", "usageTotalUsd": 0.10, "startedAt": "2026-09-21T01:00:00.000Z"},
		{"id": "out", "status": "SUCCEEDED", "usageTotalUsd": 9.00, "startedAt": "2026-10-21T00:00:00.000Z"},
	}
	srv := fake.server()
	defer srv.Close()
	snap, err := newTestApifyClient(srv.URL, "test-token", clock).AccountBudget(t.Context(), 100)
	if err != nil {
		t.Fatalf("AccountBudget: %v", err)
	}
	if fake.listDesc != "0" || fake.listLimit != "1000" {
		t.Errorf("list desc=%q limit=%q, want desc=0 limit=1000", fake.listDesc, fake.listLimit)
	}
	if snap.UsedCents != 10 {
		t.Errorf("used cents = %d, want 10 (period end exclusive)", snap.UsedCents)
	}
}

func TestRunFullScrape_DiceRoundsRepeatPairs(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	queries, err := json.Marshal([]map[string]string{
		{"keywords": "First", "location": "Port Orange, FL", "include_remote": "true"},
		{"keywords": "Second", "location": "Daytona Beach, FL", "include_remote": "false"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET enabled = true, search_queries = $1::json, global_searches = '[]'::json,
		    last_scraped_at = NULL, next_eligible_at = NULL, rounds = 2, pages_to_scrape = 1
		WHERE job_source = 'DICE'
	`, queries); err != nil {
		t.Fatalf("configure Dice: %v", err)
	}
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	srv := fake.server()
	defer srv.Close()
	s := NewService(pool)
	s.ApifyToken = "test-token"
	s.ApifyBudgetCents = 100
	s.ApifyBaseURL = srv.URL
	s.Now = clock.Now
	s.Sleep = clock.Sleep
	if _, err := s.RunFullScrape(ctx, db.SourceDice); err != nil {
		t.Fatalf("RunFullScrape: %v", err)
	}
	if fake.startCount.Load() != 4 {
		t.Errorf("starts = %d, want 4 (2 pairs × 2 rounds)", fake.startCount.Load())
	}
}

func TestRunFullScrape_DiceNonBudgetErrorUsesBackoff(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	if _, err := pool.Exec(`
		UPDATE scraper_settings SET enabled = true, last_scraped_at = NULL, next_eligible_at = NULL
		WHERE job_source = 'DICE'
	`); err != nil {
		t.Fatalf("enable Dice: %v", err)
	}
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	fake.startHTTP = http.StatusInternalServerError
	srv := fake.server()
	defer srv.Close()
	s := NewService(pool)
	s.ApifyToken = "test-token"
	s.ApifyBudgetCents = 100
	s.ApifyBaseURL = srv.URL
	s.Now = clock.Now
	s.Sleep = clock.Sleep
	s.ErrorBackoff = 5 * time.Minute
	if _, err := s.RunFullScrape(ctx, db.SourceDice); err != nil {
		t.Fatalf("RunFullScrape: %v", err)
	}
	stored, err := db.GetScraperSettings(ctx, pool, db.SourceDice)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.NextEligibleAt == nil {
		t.Fatal("non-budget Apify error must set next_eligible_at backoff")
	}
	got := stored.NextEligibleAt.UTC()
	want := time.Date(2026, 9, 22, 12, 5, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("next_eligible_at %s want injected now+5m %s", got, want)
	}
	_, periodEnd := ApifyBudgetPeriod(clock.Now())
	if got.Equal(periodEnd) {
		t.Error("non-budget error must not use period start")
	}
}

func TestRunFullScrape_DiceSkipsWhenDiceLockBusy(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	if _, err := pool.Exec(`
		UPDATE scraper_settings SET enabled = true WHERE job_source = 'DICE'
	`); err != nil {
		t.Fatalf("enable Dice: %v", err)
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	var locked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext('DICE'))`).Scan(&locked); err != nil {
		t.Fatalf("hold DICE: %v", err)
	}
	if !locked {
		t.Fatal("test must hold DICE lock")
	}
	defer func() { _, _ = conn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtext('DICE'))`) }()

	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
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
	if fake.startCount.Load() != 0 {
		t.Errorf("starts = %d, want 0 while DICE lock is held", fake.startCount.Load())
	}
	if !strings.Contains(strings.ToLower(result.Error), "lock") {
		t.Errorf("error = %q, want lock busy", result.Error)
	}
}

func TestRunFullScrape_DiceThirteenMinuteBoundary(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	if _, err := pool.Exec(`
		UPDATE scraper_settings SET enabled = true, last_scraped_at = NULL, next_eligible_at = NULL
		WHERE job_source = 'DICE'
	`); err != nil {
		t.Fatalf("enable Dice: %v", err)
	}

	t.Run("at_least_thirteen_minutes_starts", func(t *testing.T) {
		clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
		fake := newApifyFake(t)
		srv := fake.server()
		defer srv.Close()
		s := NewService(pool)
		s.ApifyToken = "test-token"
		s.ApifyBudgetCents = 100
		s.ApifyBaseURL = srv.URL
		s.Now = clock.Now
		s.Sleep = clock.Sleep
		long, cancel := context.WithDeadline(ctx, time.Now().Add(13*time.Minute+30*time.Second))
		defer cancel()
		if _, err := s.RunFullScrape(long, db.SourceDice); err != nil {
			t.Fatalf("RunFullScrape: %v", err)
		}
		if fake.startCount.Load() < 1 {
			t.Errorf("starts = %d, want at least 1 when remaining >= 13m", fake.startCount.Load())
		}
	})
	t.Run("under_thirteen_minutes_skips", func(t *testing.T) {
		clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
		fake := newApifyFake(t)
		srv := fake.server()
		defer srv.Close()
		s := NewService(pool)
		s.ApifyToken = "test-token"
		s.ApifyBudgetCents = 100
		s.ApifyBaseURL = srv.URL
		s.Now = clock.Now
		s.Sleep = clock.Sleep
		short, cancel := context.WithDeadline(ctx, time.Now().Add(13*time.Minute-time.Second))
		defer cancel()
		result, err := s.RunFullScrape(short, db.SourceDice)
		if err != nil {
			t.Fatalf("RunFullScrape: %v", err)
		}
		if fake.startCount.Load() != 0 {
			t.Errorf("starts = %d, want 0 when remaining < 13m", fake.startCount.Load())
		}
		if result.SkippedCount < 1 {
			t.Errorf("skipped_count = %d, want at least 1", result.SkippedCount)
		}
	})
}
