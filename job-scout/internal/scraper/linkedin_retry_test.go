package scraper

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/db"
)

func TestLinkedInScrapePage_Retries429ThenSucceeds(t *testing.T) {
	restore := stubLinkedInRetryDelays(t, 4*time.Second, 60*time.Second)
	defer restore()

	raw := linkedInSearchFixture(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, "slow down")
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)

	var waits []time.Duration
	s := NewLinkedIn(db.ScraperSettings{PagesToScrape: 1})
	s.client = srv.Client()
	s.sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}

	jobs, _, err := s.scrapePage(t.Context(), srv.URL)
	if err != nil {
		t.Fatalf("scrapePage: %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("GET hits = %d, want 2 (one 429 then success)", hits.Load())
	}
	if len(jobs) != 2 {
		t.Errorf("jobs = %d, want 2 from fixture after retry", len(jobs))
	}
	if len(waits) != 1 || waits[0] != 4*time.Second {
		t.Errorf("retry waits = %v, want [4s]", waits)
	}
}

func TestLinkedInScrapePage_ExponentialBackoffThenGivesUp(t *testing.T) {
	restore := stubLinkedInRetryDelays(t, 4*time.Second, 60*time.Second)
	defer restore()
	oldAttempts := linkedInRetryAttempts
	linkedInRetryAttempts = 4
	t.Cleanup(func() { linkedInRetryAttempts = oldAttempts })

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	var waits []time.Duration
	s := NewLinkedIn(db.ScraperSettings{PagesToScrape: 1})
	s.client = srv.Client()
	s.sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}

	_, _, err := s.scrapePage(t.Context(), srv.URL)
	if err == nil || err.Error() != "linkedin returned status 429" {
		t.Fatalf("err = %v, want linkedin returned status 429", err)
	}
	if hits.Load() != 4 {
		t.Errorf("GET hits = %d, want 4 attempts", hits.Load())
	}
	want := []time.Duration{4 * time.Second, 8 * time.Second, 16 * time.Second}
	if len(waits) != len(want) {
		t.Fatalf("retry waits = %v, want %v", waits, want)
	}
	for i := range want {
		if waits[i] != want[i] {
			t.Errorf("wait[%d] = %s, want %s", i, waits[i], want[i])
		}
	}
}

func TestLinkedInScrapePage_DoesNotRetry500(t *testing.T) {
	restore := stubLinkedInRetryDelays(t, 4*time.Second, 60*time.Second)
	defer restore()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	s := NewLinkedIn(db.ScraperSettings{PagesToScrape: 1})
	s.client = srv.Client()
	s.sleep = func(context.Context, time.Duration) error {
		t.Error("500 must not sleep/retry")
		return nil
	}

	_, _, err := s.scrapePage(t.Context(), srv.URL)
	if err == nil || err.Error() != "linkedin returned status 500" {
		t.Fatalf("err = %v, want linkedin returned status 500", err)
	}
	if hits.Load() != 1 {
		t.Errorf("GET hits = %d, want 1", hits.Load())
	}
}

func TestLinkedInScrapePage_HonorsRetryAfter(t *testing.T) {
	restore := stubLinkedInRetryDelays(t, 4*time.Second, 60*time.Second)
	defer restore()

	raw := linkedInSearchFixture(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "17")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)

	var waits []time.Duration
	s := NewLinkedIn(db.ScraperSettings{PagesToScrape: 1})
	s.client = srv.Client()
	s.sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}

	if _, _, err := s.scrapePage(t.Context(), srv.URL); err != nil {
		t.Fatalf("scrapePage: %v", err)
	}
	if len(waits) != 1 || waits[0] != 17*time.Second {
		t.Errorf("Retry-After waits = %v, want [17s]", waits)
	}
}

func TestLinkedInScrapePage_CapsRetryAfter(t *testing.T) {
	restore := stubLinkedInRetryDelays(t, 4*time.Second, 8*time.Second)
	defer restore()

	raw := linkedInSearchFixture(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)

	var waits []time.Duration
	s := NewLinkedIn(db.ScraperSettings{PagesToScrape: 1})
	s.client = srv.Client()
	s.sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}

	if _, _, err := s.scrapePage(t.Context(), srv.URL); err != nil {
		t.Fatalf("scrapePage: %v", err)
	}
	if len(waits) != 1 || waits[0] != 8*time.Second {
		t.Errorf("capped Retry-After waits = %v, want [8s]", waits)
	}
}

func TestLinkedInScrapePage_ReportsRateLimitProgress(t *testing.T) {
	restore := stubLinkedInRetryDelays(t, 4*time.Second, 60*time.Second)
	defer restore()

	raw := linkedInSearchFixture(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)

	var phases []string
	ctx := WithProgressReporter(t.Context(), func(p Progress) {
		phases = append(phases, p.Phase)
	})
	s := NewLinkedIn(db.ScraperSettings{PagesToScrape: 1})
	s.client = srv.Client()
	s.sleep = func(context.Context, time.Duration) error { return nil }

	if _, _, err := s.scrapePage(ctx, srv.URL); err != nil {
		t.Fatalf("scrapePage: %v", err)
	}
	found := false
	for _, phase := range phases {
		if phase == "rate_limit" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("progress phases = %v, want rate_limit heartbeat during 429 wait", phases)
	}
}

func TestFetchJobDescription_Retries429(t *testing.T) {
	restore := stubLinkedInRetryDelays(t, 4*time.Second, 60*time.Second)
	defer restore()

	raw, err := os.ReadFile(filepath.Join("testdata", "linkedin_job_detail.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)

	s := NewLinkedIn(db.ScraperSettings{})
	s.client = srv.Client()
	s.sleep = func(context.Context, time.Duration) error { return nil }

	got, err := s.FetchJobDescription(t.Context(), srv.URL+"/jobs/view/1/")
	if err != nil {
		t.Fatalf("FetchJobDescription: %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("detail GET hits = %d, want 2", hits.Load())
	}
	if !strings.Contains(got, "computer troubleshooting") {
		t.Errorf("description %q must parse after 429 retry", got)
	}
}

func TestLinkedInRetryWait_UsesBackoffWithoutHeader(t *testing.T) {
	restore := stubLinkedInRetryDelays(t, 4*time.Second, 60*time.Second)
	defer restore()
	got := linkedInRetryWait(http.Header{}, 4*time.Second)
	if got != 4*time.Second {
		t.Errorf("wait = %s, want 4s", got)
	}
}

func TestLinkedInScrapeJobs_StopsWhenLaterPageIsShorter(t *testing.T) {
	oldPause := linkedInPagePause
	linkedInPagePause = 0
	t.Cleanup(func() { linkedInPagePause = oldPause })

	var starts []string
	client, hits, restore := interceptLinkedIn(t, func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("start")
		starts = append(starts, start)
		n, base := 12, 1000000
		if start == "12" {
			n, base = 7, 2000000
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, linkedInCardsHTML(n, base))
	})
	defer restore()

	s := NewLinkedIn(db.ScraperSettings{PagesToScrape: 5})
	s.client = client
	jobs, err := s.ScrapeJobs(t.Context(), map[string]string{"keywords": "IT Help Desk"})
	if err != nil {
		t.Fatalf("ScrapeJobs: %v", err)
	}
	if *hits != 2 {
		t.Errorf("hits = %d, want 2 (shorter than first page, skip remaining 3)", *hits)
	}
	if got := strings.Join(starts, ","); got != "0,12" {
		t.Errorf("start params = %q, want 0,12", got)
	}
	if len(jobs) != 19 {
		t.Errorf("jobs = %d, want 19", len(jobs))
	}
}

func TestLinkedInScrapeJobs_StopsOnEmptyPage(t *testing.T) {
	oldPause := linkedInPagePause
	linkedInPagePause = 0
	t.Cleanup(func() { linkedInPagePause = oldPause })

	client, hits, restore := interceptLinkedIn(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("start") == "10" {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `<ul class="jobs-search__results-list"></ul>`)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, linkedInCardsHTML(10, 1000000))
	})
	defer restore()

	s := NewLinkedIn(db.ScraperSettings{PagesToScrape: 5})
	s.client = client
	jobs, err := s.ScrapeJobs(t.Context(), map[string]string{"keywords": "IT Help Desk"})
	if err != nil {
		t.Fatalf("ScrapeJobs: %v", err)
	}
	if *hits != 2 {
		t.Errorf("hits = %d, want 2 (confirm end with empty page)", *hits)
	}
	if len(jobs) != 10 {
		t.Errorf("jobs = %d, want 10", len(jobs))
	}
}

func TestLinkedInScrapeJobs_KeepsPagingWhileCountMatchesFirstPage(t *testing.T) {
	oldPause := linkedInPagePause
	linkedInPagePause = 0
	t.Cleanup(func() { linkedInPagePause = oldPause })

	client, hits, restore := interceptLinkedIn(t, func(w http.ResponseWriter, r *http.Request) {
		start, _ := strconv.Atoi(r.URL.Query().Get("start"))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, linkedInCardsHTML(8, 1000000+start))
	})
	defer restore()

	s := NewLinkedIn(db.ScraperSettings{PagesToScrape: 3})
	s.client = client
	jobs, err := s.ScrapeJobs(t.Context(), map[string]string{"keywords": "IT Help Desk"})
	if err != nil {
		t.Fatalf("ScrapeJobs: %v", err)
	}
	if *hits != 3 {
		t.Errorf("hits = %d, want 3 (same count as first page is not treated as short)", *hits)
	}
	if len(jobs) != 24 {
		t.Errorf("jobs = %d, want 24", len(jobs))
	}
}

func linkedInSearchFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "linkedin_search_cards.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

func stubLinkedInRetryDelays(t *testing.T, initial, maxWait time.Duration) func() {
	t.Helper()
	oldInitial := linkedInRetryInitial
	oldMax := linkedInRetryMaxWait
	oldChunk := linkedInRetryHeartbeat
	linkedInRetryInitial = initial
	linkedInRetryMaxWait = maxWait
	linkedInRetryHeartbeat = 0
	return func() {
		linkedInRetryInitial = oldInitial
		linkedInRetryMaxWait = oldMax
		linkedInRetryHeartbeat = oldChunk
	}
}
