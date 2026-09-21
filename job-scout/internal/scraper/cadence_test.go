package scraper

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
)

func TestNewServiceWithConfig_CopiesTimeoutAndBackoff(t *testing.T) {
	cfg := config.Config{HTTPTimeoutSeconds: 12, ScrapeErrorBackoffSeconds: 99}
	s := NewServiceWithConfig(nil, cfg)
	if s.HTTPTimeout != 12*time.Second {
		t.Errorf("HTTPTimeout = %s, want 12s from HTTP_TIMEOUT_SECONDS", s.HTTPTimeout)
	}
	if s.ErrorBackoff != 99*time.Second {
		t.Errorf("ErrorBackoff = %s, want 99s from SCRAPE_ERROR_BACKOFF_SECONDS", s.ErrorBackoff)
	}
}

func TestRunTick_NoDueProviderDoesNoLinkedInHTTP(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET last_scraped_at = now(), next_eligible_at = NULL
		WHERE job_source = 'LINKEDIN'
	`); err != nil {
		t.Errorf("last_scraped_at is required so a tick can skip a provider that is not due: %v", err)
		return
	}

	client, hits, restore := interceptLinkedIn(t, linkedInFixtureHandler(t, 200))
	defer restore()

	s := NewService(pool)
	s.HTTPClient = client
	s.ErrorBackoff = 300 * time.Second
	result, err := s.RunTick(ctx, TickInput{})
	if err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	if result.Status == "error" && strings.Contains(result.Error, "no scraper settings") {
		t.Errorf("cadence scrape must load scraper_settings: %s", result.Error)
	}
	if *hits != 0 {
		t.Errorf("tick with no due provider must not send LinkedIn HTTP, hits=%d", *hits)
	}
}

func TestRunTick_SuccessSetsLastScrapedAndClearsBackoff(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET last_scraped_at = NULL, next_eligible_at = now() + interval '1 hour'
		WHERE job_source = 'LINKEDIN'
	`); err != nil {
		t.Errorf("cadence columns are required: %v", err)
		return
	}

	client, hits, restore := interceptLinkedIn(t, linkedInFixtureHandler(t, 200))
	defer restore()

	s := NewService(pool)
	s.HTTPClient = client
	s.ErrorBackoff = 300 * time.Second
	before := time.Now().UTC().Add(-2 * time.Second)
	result, err := s.RunTick(ctx, TickInput{Force: true})
	after := time.Now().UTC().Add(2 * time.Second)
	if err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("full LinkedIn scrape status = %q (%s), want success", result.Status, result.Error)
	}
	if *hits == 0 {
		t.Error("force scrape of an enabled due provider must send LinkedIn HTTP")
	}

	var (
		last sql.NullTime
		next sql.NullTime
	)
	if err := pool.QueryRow(`
		SELECT last_scraped_at, next_eligible_at FROM scraper_settings WHERE job_source = 'LINKEDIN'
	`).Scan(&last, &next); err != nil {
		t.Errorf("successful scrape must persist last_scraped_at and clear next_eligible_at: %v", err)
		return
	}
	if !last.Valid {
		t.Error("successful LinkedIn scrape must set last_scraped_at")
	} else {
		got := last.Time.UTC()
		if got.Before(before) || got.After(after) {
			t.Errorf("last_scraped_at %s is outside scrape window [%s, %s]", got, before, after)
		}
	}
	if next.Valid {
		t.Errorf("successful scrape must clear next_eligible_at, got %s", next.Time)
	}

	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if n < 1 {
		t.Error("successful scrape must persist parsed jobs")
	}
	var filtered int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM jobs WHERE state <> 'pending' OR reject_reason IS NOT NULL`).Scan(&filtered); err != nil {
		t.Fatalf("count filtered scrape rows: %v", err)
	}
	if filtered != 0 {
		t.Errorf("scrape must not run notify filters, non-pending or rejected rows=%d", filtered)
	}
}

func TestRunTick_PartialFailurePersistsJobsAndAppliesBackoff(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	queries, err := json.Marshal([]map[string]string{
		{"keywords": "IT Help Desk", "location": "101076143"},
		{"keywords": "Application Support", "location": "101076143"},
	})
	if err != nil {
		t.Fatalf("marshal queries: %v", err)
	}
	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET search_queries = $1::json, global_searches = '[]',
		    last_scraped_at = NULL, next_eligible_at = NULL
		WHERE job_source = 'LINKEDIN'
	`, queries); err != nil {
		t.Errorf("cadence columns and search_queries must exist: %v", err)
		return
	}

	calls := 0
	client, hits, restore := interceptLinkedIn(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			linkedInFixtureHandler(t, 200)(w, r)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "nope")
	})
	defer restore()

	s := NewService(pool)
	s.HTTPClient = client
	s.ErrorBackoff = 5 * time.Minute
	before := time.Now().UTC().Add(-2 * time.Second)
	result, err := s.RunTick(ctx, TickInput{Force: true})
	after := time.Now().UTC().Add(5 * time.Minute).Add(2 * time.Second)
	if err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("partial scrape must be failure for cadence, status=%q", result.Status)
	}
	if *hits < 2 {
		t.Errorf("partial scrape must attempt the failing query, hits=%d", *hits)
	}

	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if n < 1 {
		t.Error("partial LinkedIn results must persist even when a later query fails")
	}

	var (
		last sql.NullTime
		next sql.NullTime
	)
	if err := pool.QueryRow(`
		SELECT last_scraped_at, next_eligible_at FROM scraper_settings WHERE job_source = 'LINKEDIN'
	`).Scan(&last, &next); err != nil {
		t.Errorf("failure must persist next_eligible_at backoff: %v", err)
		return
	}
	if last.Valid {
		t.Error("partial scrape must not set last_scraped_at")
	}
	if !next.Valid {
		t.Error("partial scrape must set next_eligible_at from SCRAPE_ERROR_BACKOFF_SECONDS")
	} else {
		got := next.Time.UTC()
		wantLow := before.Add(5 * time.Minute)
		if got.Before(wantLow) || got.After(after) {
			t.Errorf("next_eligible_at %s want now+5m in [%s, %s]", got, wantLow, after)
		}
	}
}

func TestRunTick_PartialPageFailurePersistsJobsAndAppliesBackoff(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	if _, err := pool.Exec(`
		UPDATE scraper_settings SET pages_to_scrape = 2, global_searches = '[]',
		    last_scraped_at = NULL, next_eligible_at = NULL
		WHERE job_source = 'LINKEDIN'
	`); err != nil {
		t.Errorf("cadence columns are required: %v", err)
		return
	}

	client, hits, restore := interceptLinkedIn(t, func(w http.ResponseWriter, r *http.Request) {
		if start := r.URL.Query().Get("start"); start != "" && start != "0" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "page 2 failed")
			return
		}
		linkedInFixtureHandler(t, 200)(w, r)
	})
	defer restore()

	s := NewService(pool)
	s.HTTPClient = client
	s.ErrorBackoff = 5 * time.Minute
	result, err := s.RunTick(ctx, TickInput{Force: true})
	if err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("page-2 failure must be cadence failure, status=%q", result.Status)
	}
	if *hits < 2 {
		t.Errorf("must fetch page 2, hits=%d", *hits)
	}
	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if n < 1 {
		t.Error("jobs from page 1 must persist when page 2 fails")
	}
	var last, next sql.NullTime
	if err := pool.QueryRow(`
		SELECT last_scraped_at, next_eligible_at FROM scraper_settings WHERE job_source = 'LINKEDIN'
	`).Scan(&last, &next); err != nil {
		t.Errorf("page failure must persist backoff: %v", err)
		return
	}
	if last.Valid {
		t.Error("page-2 failure must not set last_scraped_at")
	}
	if !next.Valid {
		t.Error("page-2 failure must set next_eligible_at")
	}
}

func TestRunTick_LinkedIn429RetriesThenSucceeds(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET last_scraped_at = NULL, next_eligible_at = NULL, global_searches = '[]'
		WHERE job_source = 'LINKEDIN'
	`); err != nil {
		t.Fatalf("configure LinkedIn: %v", err)
	}

	calls := 0
	client, hits, restore := interceptLinkedIn(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, "slow down")
			return
		}
		linkedInFixtureHandler(t, 200)(w, r)
	})
	defer restore()

	s := NewService(pool)
	s.HTTPClient = client
	result, err := s.RunTick(ctx, TickInput{Force: true})
	if err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q (%s), want success after 429 retry", result.Status, result.Error)
	}
	if *hits < 2 {
		t.Errorf("429 must retry the same GET, hits=%d", *hits)
	}
	var next sql.NullTime
	if err := pool.QueryRow(`
		SELECT next_eligible_at FROM scraper_settings WHERE job_source = 'LINKEDIN'
	`).Scan(&next); err != nil {
		t.Fatalf("read next_eligible_at: %v", err)
	}
	if next.Valid {
		t.Errorf("successful 429 retry must not set next_eligible_at, got %s", next.Time)
	}
}

func TestRunTick_RoundsRepeatFullPassAndKeepPaging(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	queries, err := json.Marshal([]map[string]string{
		{"keywords": "IT Help Desk", "location": "101076143"},
		{"keywords": "Application Support", "location": "101076143"},
	})
	if err != nil {
		t.Fatalf("marshal queries: %v", err)
	}
	global, err := json.Marshal([]string{"Remote IT Help Desk near Port Orange FL"})
	if err != nil {
		t.Fatalf("marshal global searches: %v", err)
	}
	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET search_queries = $1::json, global_searches = $2::json,
		    pages_to_scrape = 2, rounds = 1
		WHERE job_source = 'LINKEDIN'
	`, queries, global); err != nil {
		t.Fatalf("configure rounds test: %v", err)
	}

	client, hits, restore := interceptLinkedIn(t, func(w http.ResponseWriter, r *http.Request) {
		linkedInFixtureHandler(t, http.StatusOK)(w, r)
	})
	defer restore()

	s := NewService(pool)
	s.HTTPClient = client
	if result, err := s.RunTick(ctx, TickInput{Force: true}); err != nil {
		t.Fatalf("one-round RunTick: %v", err)
	} else if result.Status != "success" {
		t.Fatalf("one-round RunTick status = %q (%s), want success", result.Status, result.Error)
	}
	const requestsPerRound = 1*2 + 2*2 // one global search (2 pages) + two location queries (2 pages each)
	if *hits != requestsPerRound {
		t.Errorf("one round requests = %d, want %d", *hits, requestsPerRound)
	}

	if _, err := pool.Exec(`
		UPDATE scraper_settings SET rounds = 3 WHERE job_source = 'LINKEDIN'
	`); err != nil {
		t.Fatalf("set three rounds: %v", err)
	}
	before := *hits
	if result, err := s.RunTick(ctx, TickInput{Force: true}); err != nil {
		t.Fatalf("three-round RunTick: %v", err)
	} else if result.Status != "success" {
		t.Fatalf("three-round RunTick status = %q (%s), want success", result.Status, result.Error)
	}
	if got, want := *hits-before, 3*requestsPerRound; got != want {
		t.Errorf("three rounds requests = %d, want %d", got, want)
	}
}

func TestRunTick_ForceDoesNotFetchIndeed(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	if _, err := pool.Exec(`
		UPDATE scraper_settings SET last_scraped_at = now() WHERE job_source = 'LINKEDIN'
	`); err != nil {
		t.Errorf("cadence columns are required: %v", err)
		return
	}

	var urls []string
	client, hits, restore := interceptLinkedIn(t, func(w http.ResponseWriter, r *http.Request) {
		urls = append(urls, r.URL.String())
		linkedInFixtureHandler(t, 200)(w, r)
	})
	defer restore()

	s := NewService(pool)
	s.HTTPClient = client
	result, err := s.RunTick(ctx, TickInput{Force: true})
	if err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	if *hits == 0 {
		t.Errorf("force must scrape enabled LinkedIn, status=%s err=%s", result.Status, result.Error)
	}
	for _, u := range urls {
		if strings.Contains(strings.ToLower(u), "indeed") {
			t.Errorf("force must not fetch Indeed, url=%s", u)
		}
	}

	var indeedLast sql.NullTime
	if err := pool.QueryRow(`
		SELECT last_scraped_at FROM scraper_settings WHERE job_source = 'INDEED'
	`).Scan(&indeedLast); err != nil {
		t.Errorf("Indeed last_scraped_at must exist: %v", err)
	} else if indeedLast.Valid {
		t.Error("force must not update Indeed last_scraped_at")
	}
}

func TestRunTick_CombinesDueProviderResults(t *testing.T) {
	t.Run("two_due_runs_second_after_error", func(t *testing.T) {
		pool := cadencePool(t)
		ctx := t.Context()
		queries, err := json.Marshal([]map[string]string{
			{"keywords": "IT Help Desk", "location": "101076143"},
			{"keywords": "Application Support", "location": "101076143"},
		})
		if err != nil {
			t.Fatalf("marshal queries: %v", err)
		}
		if _, err := pool.Exec(`
			UPDATE scraper_settings
			SET enabled = true, search_queries = $1::json, global_searches = '[]',
			    last_scraped_at = NULL, next_eligible_at = NULL
			WHERE job_source = 'LINKEDIN'
		`, queries); err != nil {
			t.Fatalf("configure LinkedIn: %v", err)
		}
		if _, err := pool.Exec(`
			UPDATE scraper_settings SET enabled = false WHERE job_source IN ('INDEED', 'DICE', 'FANTASTIC')
		`); err != nil {
			t.Fatalf("keep Apify providers off for LinkedIn-only combine test: %v", err)
		}
		if _, err := pool.Exec(`
			INSERT INTO jobs (job_source, title, company, location, job_url)
			VALUES ('LINKEDIN', 'IT Help Desk', 'Acme Corp', 'New York, United States',
			        'https://www.linkedin.com/jobs/view/4123456789/')
		`); err != nil {
			t.Fatalf("seed duplicate job: %v", err)
		}

		okHandler := linkedInFixtureHandler(t, http.StatusOK)
		calls := 0
		client, hits, restore := interceptLinkedIn(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				okHandler(w, r)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "nope")
		})
		defer restore()

		s := NewService(pool)
		s.HTTPClient = client
		s.ErrorBackoff = 5 * time.Minute
		result, err := s.RunTick(ctx, TickInput{})
		if err != nil {
			t.Fatalf("RunTick: %v", err)
		}
		if *hits < 2 {
			t.Errorf("LinkedIn must continue after an earlier query failure, hits=%d", *hits)
		}
		if result.Status != "error" {
			t.Errorf("combined status = %q, want error when any provider failed", result.Status)
		}
		if result.JobSource != "LINKEDIN" {
			t.Errorf("job_source = %q, want LINKEDIN for single due provider with query errors", result.JobSource)
		}
		wantErr := "linkedin returned status 500"
		if result.Error != wantErr {
			t.Errorf("error = %q, want %q", result.Error, wantErr)
		}
		if result.ScrapedCount != 2 {
			t.Errorf("scraped_count = %d, want 2", result.ScrapedCount)
		}
		if result.SavedCount != 1 {
			t.Errorf("saved_count = %d, want 1", result.SavedCount)
		}
		if result.DuplicateCount != 1 {
			t.Errorf("duplicate_count = %d, want 1", result.DuplicateCount)
		}
	})

	t.Run("single_due_keeps_job_source", func(t *testing.T) {
		pool := cadencePool(t)
		ctx := t.Context()
		if _, err := pool.Exec(`
			UPDATE scraper_settings
			SET enabled = true, last_scraped_at = NULL, next_eligible_at = NULL
			WHERE job_source = 'LINKEDIN'
		`); err != nil {
			t.Fatalf("configure LinkedIn: %v", err)
		}
		if _, err := pool.Exec(`
			UPDATE scraper_settings SET enabled = false WHERE job_source = 'INDEED'
		`); err != nil {
			t.Fatalf("disable Indeed: %v", err)
		}

		okHandler := linkedInFixtureHandler(t, http.StatusOK)
		client, _, restore := interceptLinkedIn(t, okHandler)
		defer restore()

		s := NewService(pool)
		s.HTTPClient = client
		result, err := s.RunTick(ctx, TickInput{})
		if err != nil {
			t.Fatalf("RunTick: %v", err)
		}
		if result.Status != "success" {
			t.Errorf("single LinkedIn tick status = %q (%s), want success", result.Status, result.Error)
		}
		if result.JobSource != "LINKEDIN" {
			t.Errorf("single due provider job_source = %q, want LINKEDIN", result.JobSource)
		}
	})
}

func TestRunFullScrape_IndeedWithoutTokenDoesNotUpdateLastScrapedAt(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	if _, err := pool.Exec(`
		UPDATE scraper_settings SET enabled = true WHERE job_source = 'INDEED'
	`); err != nil {
		t.Fatalf("enable Indeed: %v", err)
	}

	s := NewService(pool)
	var lastBefore, nextBefore sql.NullTime
	if err := pool.QueryRow(`
		SELECT last_scraped_at, next_eligible_at FROM scraper_settings WHERE job_source = 'INDEED'
	`).Scan(&lastBefore, &nextBefore); err != nil {
		t.Errorf("Indeed cadence columns must exist: %v", err)
		return
	}

	result, err := s.RunFullScrape(ctx, db.SourceIndeed)
	if err != nil {
		t.Fatalf("RunFullScrape Indeed: %v", err)
	}
	if result.Status != "skipped" {
		t.Errorf("status = %q, want skipped without Apify token", result.Status)
	}
	if !strings.Contains(result.Error, "Apify") {
		t.Errorf("error = %q, want Apify setup message", result.Error)
	}

	var last, next sql.NullTime
	if err := pool.QueryRow(`
		SELECT last_scraped_at, next_eligible_at FROM scraper_settings WHERE job_source = 'INDEED'
	`).Scan(&last, &next); err != nil {
		t.Errorf("Indeed last_scraped_at must exist: %v", err)
		return
	}
	if last.Valid != lastBefore.Valid || next.Valid != nextBefore.Valid {
		t.Error("Indeed without token must not change last_scraped_at or next_eligible_at")
	}
}

func TestRunTick_SkipsWhenAdvisoryLockBusy(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()

	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()

	var locked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext('LINKEDIN'))`).Scan(&locked); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	if !locked {
		t.Fatal("test must hold the LinkedIn advisory lock")
	}
	defer func() {
		_, _ = conn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtext('LINKEDIN'))`)
	}()

	client, hits, restore := interceptLinkedIn(t, linkedInFixtureHandler(t, 200))
	defer restore()

	s := NewService(pool)
	s.HTTPClient = client
	result, err := s.RunTick(ctx, TickInput{Force: true})
	if err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	if *hits != 0 {
		t.Errorf("must skip LinkedIn HTTP when advisory lock is held, hits=%d status=%s", *hits, result.Status)
	}
}

func TestRunFullScrape_CancelledContextReleasesAdvisoryLock(t *testing.T) {
	pool := cadencePool(t)
	started := make(chan struct{})
	var once sync.Once
	client, _, restore := interceptLinkedIn(t, func(_ http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		<-r.Context().Done()
	})
	defer restore()

	s := NewService(pool)
	s.HTTPClient = client
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.RunFullScrape(ctx, db.SourceLinkedIn)
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("scrape did not start")
	}
	probe, err := pool.Conn(t.Context())
	if err != nil {
		t.Fatalf("probe conn: %v", err)
	}
	defer probe.Close()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled scrape did not return")
	}

	var locked bool
	if err := probe.QueryRowContext(t.Context(),
		`SELECT pg_try_advisory_lock(hashtext('LINKEDIN'))`).Scan(&locked); err != nil {
		t.Fatalf("try lock after cancellation: %v", err)
	}
	if !locked {
		t.Error("cancelled scrape left the provider advisory lock held")
	}
	if locked {
		_, _ = probe.ExecContext(t.Context(),
			`SELECT pg_catalog.pg_advisory_unlock(hashtext('LINKEDIN'))`)
	}
}

func TestRunFullScrape_FailedUnlockDiscardsLockedConnection(t *testing.T) {
	pool := cadencePool(t)
	if _, err := pool.Exec(`
		CREATE SEQUENCE public.unlock_attempts;
		CREATE FUNCTION public.pg_advisory_unlock(integer) RETURNS boolean
		LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM nextval('public.unlock_attempts');
			RAISE EXCEPTION 'forced unlock failure';
		END
		$$
	`); err != nil {
		t.Fatalf("install failing unlock function: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	client, _, restore := interceptLinkedIn(t, func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		<-release
		linkedInFixtureHandler(t, http.StatusOK)(w, r)
	})
	defer restore()

	s := NewService(pool)
	s.HTTPClient = client
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.RunFullScrape(t.Context(), db.SourceLinkedIn)
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("scrape did not start")
	}
	probe, err := pool.Conn(t.Context())
	if err != nil {
		t.Fatalf("probe conn: %v", err)
	}
	defer probe.Close()
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("scrape did not return")
	}

	var unlockAttempted bool
	if err := probe.QueryRowContext(t.Context(),
		`SELECT is_called FROM public.unlock_attempts`).Scan(&unlockAttempted); err != nil {
		t.Fatalf("read unlock attempt: %v", err)
	}
	if !unlockAttempted {
		t.Fatal("test did not exercise the forced unlock failure")
	}

	var locked bool
	if err := probe.QueryRowContext(t.Context(),
		`SELECT pg_try_advisory_lock(hashtext('LINKEDIN'))`).Scan(&locked); err != nil {
		t.Fatalf("try lock after failed unlock: %v", err)
	}
	if !locked {
		t.Error("failed unlock returned a pooled connection with the provider lock held")
	}
	if locked {
		_, _ = probe.ExecContext(t.Context(),
			`SELECT pg_catalog.pg_advisory_unlock(hashtext('LINKEDIN'))`)
	}
}

func TestRunFullScrape_DebugForceStillSkipsDisabledAndTakesLock(t *testing.T) {
	pool := cadencePool(t)
	ctx := t.Context()
	if _, err := pool.Exec(`
		UPDATE scraper_settings SET last_scraped_at = now() WHERE job_source = 'LINKEDIN'
	`); err != nil {
		t.Errorf("cadence columns are required: %v", err)
		return
	}

	client, hits, restore := interceptLinkedIn(t, linkedInFixtureHandler(t, 200))
	defer restore()

	s := NewService(pool)
	s.HTTPClient = client
	result, err := s.RunFullScrape(ctx, db.SourceLinkedIn)
	if err != nil {
		t.Fatalf("debug scrape: %v", err)
	}
	if *hits == 0 {
		t.Errorf("debug scrape may ignore cadence (force) for enabled LinkedIn, status=%s err=%s", result.Status, result.Error)
	}

	indeed, err := s.RunFullScrape(ctx, db.SourceIndeed)
	if err != nil {
		t.Fatalf("debug scrape Indeed: %v", err)
	}
	if indeed.Status == "success" {
		t.Error("debug scrape must not succeed for disabled Indeed")
	}
	if indeed.Status != "skipped" {
		t.Errorf("disabled Indeed status = %q, want skipped", indeed.Status)
	}
}

func cadencePool(t *testing.T) *sql.DB {
	t.Helper()
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := t.Context()
	if err := db.SeedSettings(ctx, pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	oldSleep := sleepBetween
	oldPause := linkedInPagePause
	oldRetryInitial := linkedInRetryInitial
	sleepBetween = 0
	linkedInPagePause = 0
	linkedInRetryInitial = 0
	t.Cleanup(func() {
		sleepBetween = oldSleep
		linkedInPagePause = oldPause
		linkedInRetryInitial = oldRetryInitial
	})
	queries, err := json.Marshal([]map[string]string{
		{"keywords": "IT Help Desk", "location": "101076143"},
	})
	if err != nil {
		t.Fatalf("marshal queries: %v", err)
	}
	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET search_queries = $1::json, global_searches = '[]', pages_to_scrape = 1
		WHERE job_source = 'LINKEDIN'
	`, queries); err != nil {
		t.Fatalf("shrink LinkedIn queries for tests: %v", err)
	}
	if _, err := pool.Exec(`
		UPDATE scraper_settings SET enabled = false WHERE job_source IN ('DICE', 'INDEED', 'FANTASTIC')
	`); err != nil {
		t.Fatalf("disable Apify providers so LinkedIn cadence tests stay isolated: %v", err)
	}
	return pool
}

func linkedInFixtureHandler(t *testing.T, status int) http.HandlerFunc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "linkedin_search_cards.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(raw)
	}
}

func linkedInCardsHTML(n, idBase int) string {
	var b strings.Builder
	b.WriteString(`<ul class="jobs-search__results-list">`)
	for i := 0; i < n; i++ {
		id := strconv.Itoa(idBase + i)
		b.WriteString(`<li><div class="base-card base-search-card job-search-card" data-entity-urn="urn:li:jobPosting:`)
		b.WriteString(id)
		b.WriteString(`"><div class="base-search-card__info"><h3 class="base-search-card__title">Job `)
		b.WriteString(id)
		b.WriteString(`</h3><h4 class="base-search-card__subtitle"><a class="hidden-nested-link">Co</a></h4>`)
		b.WriteString(`<span class="job-search-card__location">Remote</span>`)
		b.WriteString(`<time datetime="2026-09-18">1 day ago</time></div></div></li>`)
	}
	b.WriteString(`</ul>`)
	return b.String()
}

func interceptLinkedIn(t *testing.T, h http.HandlerFunc) (*http.Client, *int, func()) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		h(w, r)
	}))
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse httptest url: %v", err)
	}
	orig := http.DefaultTransport
	rt := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clone.URL.Scheme = target.Scheme
		clone.URL.Host = target.Host
		clone.RequestURI = ""
		return orig.RoundTrip(clone)
	})
	http.DefaultTransport = rt
	client := &http.Client{Timeout: 30 * time.Second, Transport: rt}
	return client, &hits, func() {
		http.DefaultTransport = orig
		srv.Close()
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
