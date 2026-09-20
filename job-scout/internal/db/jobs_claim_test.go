package db

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestClaimReadyJobs_TakesOldestUpToLimitAndLeavesLeftovers(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	urls := []string{
		"https://www.linkedin.com/jobs/view/claim-old/",
		"https://www.linkedin.com/jobs/view/claim-mid/",
		"https://www.linkedin.com/jobs/view/claim-new/",
	}
	for _, u := range urls {
		if _, err := InsertJobIfNew(ctx, pool, sampleJob(u)); err != nil {
			t.Fatalf("insert %s: %v", u, err)
		}
	}
	if _, err := pool.Exec(`UPDATE jobs SET state = 'ready'`); err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET created_at = now() - interval '3 minutes' WHERE job_url LIKE '%claim-old/'`); err != nil {
		t.Fatalf("age old: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET created_at = now() - interval '2 minutes' WHERE job_url LIKE '%claim-mid/'`); err != nil {
		t.Fatalf("age mid: %v", err)
	}

	claimed, err := ClaimReadyJobs(ctx, pool, 2, 0)
	if err != nil {
		t.Fatalf("ClaimReadyJobs: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed = %d, want 2 (NOTIFY_MAX_JOBS batch)", len(claimed))
	}
	if claimed[0].JobURL != urls[0] || claimed[1].JobURL != urls[1] {
		t.Errorf("claimed URLs = %s, %s, want oldest then next (%s, %s)", claimed[0].JobURL, claimed[1].JobURL, urls[0], urls[1])
	}
	for _, j := range claimed {
		if j.State != JobStateReady || j.NotifiedAt == nil || j.NotifyClaimedAt == nil {
			t.Errorf("claimed %s = %+v, want ready with claim markers", j.JobURL, j)
		}
	}

	jobs, err := ListJobsForNotify(ctx, pool)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byURL := map[string]Job{}
	for _, j := range jobs {
		byURL[j.JobURL] = j
	}
	if byURL[urls[0]].NotifiedAt == nil || byURL[urls[1]].NotifiedAt == nil {
		t.Errorf("claimed rows = %v, want notified_at for the batch", byURL)
	}
	if byURL[urls[2]].State != JobStateReady || byURL[urls[2]].NotifiedAt != nil {
		t.Errorf("leftover %s = %+v, want unemailed ready for next notify", urls[2], byURL[urls[2]])
	}
}

func TestClaimReadyJobs_SkipLockedDoesNotWaitOnLockedReady(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/lock-a/")); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/lock-b/")); err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET state = 'ready'`); err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET created_at = now() - interval '2 minutes' WHERE job_url LIKE '%lock-a/'`); err != nil {
		t.Fatalf("age a: %v", err)
	}

	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	var lockedID int64
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM jobs WHERE job_url LIKE '%lock-a/' FOR UPDATE
	`).Scan(&lockedID); err != nil {
		t.Fatalf("lock a: %v", err)
	}

	done := make(chan []Job, 1)
	errCh := make(chan error, 1)
	go func() {
		claimed, err := ClaimReadyJobs(ctx, pool, 2, 0)
		if err != nil {
			errCh <- err
			return
		}
		done <- claimed
	}()

	select {
	case err := <-errCh:
		t.Fatalf("ClaimReadyJobs: %v", err)
	case claimed := <-done:
		if len(claimed) != 1 {
			t.Fatalf("SKIP LOCKED claimed = %d, want 1 (locked oldest row skipped)", len(claimed))
		}
		if claimed[0].JobURL != "https://www.linkedin.com/jobs/view/lock-b/" {
			t.Errorf("claimed URL = %q, want lock-b (not the locked row)", claimed[0].JobURL)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ClaimReadyJobs blocked on a locked ready row; want FOR UPDATE SKIP LOCKED")
	}
}

func TestClaimReadyJobs_ConcurrentClaimsDoNotShareJobs(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/race-a/")); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/race-b/")); err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET state = 'ready'`); err != nil {
		t.Fatalf("mark ready: %v", err)
	}

	var wg sync.WaitGroup
	got := make(chan []int64, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := ClaimReadyJobs(ctx, pool, 1, 0)
			if err != nil {
				errs <- err
				return
			}
			ids := make([]int64, len(claimed))
			for i, j := range claimed {
				ids[i] = j.ID
			}
			got <- ids
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent ClaimReadyJobs hung")
	}
	close(got)
	close(errs)
	for err := range errs {
		t.Fatalf("ClaimReadyJobs: %v", err)
	}

	var all []int64
	for ids := range got {
		all = append(all, ids...)
	}
	if len(all) != 2 {
		t.Fatalf("concurrent claims took %d jobs, want 2 (one each)", len(all))
	}
	if all[0] == all[1] {
		t.Errorf("two claims posted the same job id %d", all[0])
	}
}

func TestClaimReadyJobs_TieBreaksEqualCreatedAtByLowerID(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/tie-a/")); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/tie-b/")); err != nil {
		t.Fatalf("insert b: %v", err)
	}
	fixed := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(`UPDATE jobs SET state = 'ready', created_at = $1`, fixed); err != nil {
		t.Fatalf("same created_at: %v", err)
	}

	var lowID, highID int64
	if err := pool.QueryRow(`SELECT MIN(id), MAX(id) FROM jobs`).Scan(&lowID, &highID); err != nil {
		t.Fatalf("ids: %v", err)
	}
	claimed, err := ClaimReadyJobs(ctx, pool, 1, 0)
	if err != nil {
		t.Fatalf("ClaimReadyJobs: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %d, want 1", len(claimed))
	}
	if claimed[0].ID != lowID {
		t.Errorf("tie-break claimed id = %d, want lower id %d (not %d)", claimed[0].ID, lowID, highID)
	}
}

func TestClaimReadyJobs_ZeroRowsWhenNoneReady(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/pending-only/")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	claimed, err := ClaimReadyJobs(ctx, pool, 25, 0)
	if err != nil {
		t.Fatalf("ClaimReadyJobs: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("claimed = %d, want 0 when no ready rows", len(claimed))
	}
}

func TestClaimReadyJobs_TimeoutZeroDoesNotReclaimClaimedRow(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/stuck-claim/")); err != nil {
		t.Fatalf("insert claimed: %v", err)
	}
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/still-ready/")); err != nil {
		t.Fatalf("insert ready: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET state = 'ready', notify_claimed_at = now() - interval '1 day' WHERE job_url LIKE '%stuck-claim/'`); err != nil {
		t.Fatalf("set old claim: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET state = 'ready' WHERE job_url LIKE '%still-ready/'`); err != nil {
		t.Fatalf("mark ready: %v", err)
	}

	claimed, err := ClaimReadyJobs(ctx, pool, 25, 0)
	if err != nil {
		t.Fatalf("ClaimReadyJobs: %v", err)
	}
	if len(claimed) != 1 || claimed[0].JobURL != "https://www.linkedin.com/jobs/view/still-ready/" {
		t.Errorf("claimed = %+v, want only the unclaimed ready row", claimedURLs(claimed))
	}

	var notifiedAt *time.Time
	if err := pool.QueryRow(`SELECT notified_at FROM jobs WHERE job_url LIKE '%stuck-claim/'`).Scan(&notifiedAt); err != nil {
		t.Fatalf("stuck marker: %v", err)
	}
	if notifiedAt != nil {
		t.Errorf("stuck claim notified_at = %v, want null because timeout 0 does not reclaim", notifiedAt)
	}
}
func TestClaimWithoutPostLeavesReadyWithNotifiedMarker(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/crash-claim/")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET state = 'ready'`); err != nil {
		t.Fatalf("ready: %v", err)
	}
	claimed, err := ClaimReadyJobs(ctx, pool, 25, 0)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %d, want 1", len(claimed))
	}
	var state string
	var notifiedAt time.Time
	if err := pool.QueryRow(`SELECT state, notified_at FROM jobs WHERE id = $1`, claimed[0].ID).Scan(&state, &notifiedAt); err != nil {
		t.Fatalf("claim markers: %v", err)
	}
	if state != JobStateReady || notifiedAt.IsZero() {
		t.Errorf("claim-before-POST state=%q notified_at=%s, want ready with marker", state, notifiedAt)
	}
}

func claimedURLs(jobs []Job) []string {
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.JobURL
	}
	return out
}
