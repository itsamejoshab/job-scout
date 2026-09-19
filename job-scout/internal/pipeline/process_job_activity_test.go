package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
	"github.com/jobscout/jobscout/internal/scraper"
)

func TestGetJobDescription_UsesSourceLockAndReturnsFlags(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	held, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("connection: %v", err)
	}
	defer held.Close()
	locked, err := db.TryLockJobSource(ctx, held, db.SourceLinkedIn)
	if err != nil || !locked {
		t.Fatalf("hold LinkedIn lock: locked=%v err=%v", locked, err)
	}

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	acts := &Activities{DB: pool, Scraper: scraper.NewService(pool)}
	acts.Scraper.HTTPClient = server.Client()
	busy, err := acts.get_job_description(ctx, DetailFetchInput{
		JobURL: server.URL, JobSource: string(db.SourceLinkedIn),
	})
	if err != nil {
		t.Fatalf("busy detail result: %v", err)
	}
	if !busy.LockBusy || busy.FetchFailed || hits.Load() != 0 {
		t.Errorf("busy result=%+v HTTP hits=%d, want LockBusy and no GET", busy, hits.Load())
	}

	if err := db.UnlockJobSource(ctx, held, db.SourceLinkedIn); err != nil {
		t.Fatalf("release held lock: %v", err)
	}
	failed, err := acts.get_job_description(ctx, DetailFetchInput{
		JobURL: server.URL, JobSource: string(db.SourceLinkedIn),
	})
	if err != nil {
		t.Fatalf("empty detail result: %v", err)
	}
	if !failed.FetchFailed || failed.LockBusy || hits.Load() != 1 {
		t.Errorf("empty result=%+v HTTP hits=%d, want FetchFailed after one GET", failed, hits.Load())
	}

	check, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("check connection: %v", err)
	}
	defer check.Close()
	released, err := db.TryLockJobSource(ctx, check, db.SourceLinkedIn)
	if err != nil || !released {
		t.Errorf("GET activity left lock held: acquired=%v err=%v", released, err)
	}
	if released {
		_ = db.UnlockJobSource(ctx, check, db.SourceLinkedIn)
	}
}
