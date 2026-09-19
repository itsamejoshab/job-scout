package db

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestClaimEligibleJobs_TakesOldestUpToLimitAndLeavesLeftovers(t *testing.T) {
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
	if _, err := pool.Exec(`UPDATE jobs SET state = 'eligible'`); err != nil {
		t.Fatalf("mark eligible: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET created_at = now() - interval '3 minutes' WHERE job_url LIKE '%claim-old/'`); err != nil {
		t.Fatalf("age old: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET created_at = now() - interval '2 minutes' WHERE job_url LIKE '%claim-mid/'`); err != nil {
		t.Fatalf("age mid: %v", err)
	}

	claimed, err := ClaimEligibleJobs(ctx, pool, 2)
	if err != nil {
		t.Fatalf("ClaimEligibleJobs: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed = %d, want 2 (NOTIFY_MAX_JOBS batch)", len(claimed))
	}
	if claimed[0].JobURL != urls[0] || claimed[1].JobURL != urls[1] {
		t.Errorf("claimed URLs = %s, %s, want oldest then next (%s, %s)", claimed[0].JobURL, claimed[1].JobURL, urls[0], urls[1])
	}
	for _, j := range claimed {
		if j.State != JobStateNotifying {
			t.Errorf("claimed %s state = %q, want notifying", j.JobURL, j.State)
		}
	}

	jobs, err := ListJobsForNotify(ctx, pool)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byURL := map[string]string{}
	for _, j := range jobs {
		byURL[j.JobURL] = j.State
	}
	if byURL[urls[0]] != JobStateNotifying || byURL[urls[1]] != JobStateNotifying {
		t.Errorf("claimed states = %v, want notifying for the batch", byURL)
	}
	if byURL[urls[2]] != JobStateEligible {
		t.Errorf("leftover %s state = %q, want eligible for the next notify", urls[2], byURL[urls[2]])
	}
}

func TestClaimEligibleJobs_SkipLockedDoesNotWaitOnLockedEligible(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/lock-a/")); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/lock-b/")); err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET state = 'eligible'`); err != nil {
		t.Fatalf("mark eligible: %v", err)
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
		claimed, err := ClaimEligibleJobs(ctx, pool, 2)
		if err != nil {
			errCh <- err
			return
		}
		done <- claimed
	}()

	select {
	case err := <-errCh:
		t.Fatalf("ClaimEligibleJobs: %v", err)
	case claimed := <-done:
		if len(claimed) != 1 {
			t.Fatalf("SKIP LOCKED claimed = %d, want 1 (locked oldest row skipped)", len(claimed))
		}
		if claimed[0].JobURL != "https://www.linkedin.com/jobs/view/lock-b/" {
			t.Errorf("claimed URL = %q, want lock-b (not the locked row)", claimed[0].JobURL)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ClaimEligibleJobs blocked on a locked eligible row; want FOR UPDATE SKIP LOCKED")
	}
}

func TestClaimEligibleJobs_ConcurrentClaimsDoNotShareJobs(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/race-a/")); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/race-b/")); err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET state = 'eligible'`); err != nil {
		t.Fatalf("mark eligible: %v", err)
	}

	var wg sync.WaitGroup
	got := make(chan []int64, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := ClaimEligibleJobs(ctx, pool, 1)
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
		t.Fatal("concurrent ClaimEligibleJobs hung")
	}
	close(got)
	close(errs)
	for err := range errs {
		t.Fatalf("ClaimEligibleJobs: %v", err)
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

func TestClaimEligibleJobs_TieBreaksEqualCreatedAtByLowerID(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/tie-a/")); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/tie-b/")); err != nil {
		t.Fatalf("insert b: %v", err)
	}
	fixed := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(`UPDATE jobs SET state = 'eligible', created_at = $1`, fixed); err != nil {
		t.Fatalf("same created_at: %v", err)
	}

	var lowID, highID int64
	if err := pool.QueryRow(`SELECT MIN(id), MAX(id) FROM jobs`).Scan(&lowID, &highID); err != nil {
		t.Fatalf("ids: %v", err)
	}
	claimed, err := ClaimEligibleJobs(ctx, pool, 1)
	if err != nil {
		t.Fatalf("ClaimEligibleJobs: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %d, want 1", len(claimed))
	}
	if claimed[0].ID != lowID {
		t.Errorf("tie-break claimed id = %d, want lower id %d (not %d)", claimed[0].ID, lowID, highID)
	}
}

func TestClaimEligibleJobs_ZeroRowsWhenNoneEligible(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/pending-only/")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	claimed, err := ClaimEligibleJobs(ctx, pool, 25)
	if err != nil {
		t.Fatalf("ClaimEligibleJobs: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("claimed = %d, want 0 when no eligible rows", len(claimed))
	}
}

func TestClaimEligibleJobs_DoesNotUnstickNotifyingWhenTimeoutIsZero(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/stuck-notifying/")); err != nil {
		t.Fatalf("insert notifying: %v", err)
	}
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/still-eligible/")); err != nil {
		t.Fatalf("insert eligible: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET state = 'notifying', state_changed_at = now() - interval '1 day' WHERE job_url LIKE '%stuck-notifying/'`); err != nil {
		t.Fatalf("stick notifying: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET state = 'eligible' WHERE job_url LIKE '%still-eligible/'`); err != nil {
		t.Fatalf("mark eligible: %v", err)
	}

	claimed, err := ClaimEligibleJobs(ctx, pool, 25)
	if err != nil {
		t.Fatalf("ClaimEligibleJobs: %v", err)
	}
	if len(claimed) != 1 || claimed[0].JobURL != "https://www.linkedin.com/jobs/view/still-eligible/" {
		t.Errorf("claimed = %+v, want only the eligible row (timeout 0 does not auto-return notifying)", claimedURLs(claimed))
	}

	var stuck string
	if err := pool.QueryRow(`SELECT state FROM jobs WHERE job_url LIKE '%stuck-notifying/'`).Scan(&stuck); err != nil {
		t.Fatalf("stuck state: %v", err)
	}
	if stuck != JobStateNotifying {
		t.Errorf("stuck state = %q, want notifying (NOTIFY_CLAIM_TIMEOUT_SECONDS=0, no auto-unstick)", stuck)
	}
}

func TestLoadNotifyInventory_AfterClaimIncludesNotifyingNow(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	seed := []struct {
		url    string
		state  string
		reason *string
	}{
		{"https://www.linkedin.com/jobs/view/inv-pending/", JobStatePending, nil},
		{"https://www.linkedin.com/jobs/view/inv-title/", JobStateRejected, strPtr("title_company")},
		{"https://www.linkedin.com/jobs/view/inv-desc/", JobStateRejected, strPtr("description")},
		{"https://www.linkedin.com/jobs/view/inv-dup/", JobStateRejected, strPtr("duplicate")},
		{"https://www.linkedin.com/jobs/view/inv-detail/", JobStateRejected, strPtr("detail_failed")},
		{"https://www.linkedin.com/jobs/view/inv-eligible/", JobStateEligible, nil},
		{"https://www.linkedin.com/jobs/view/inv-notifying/", JobStateNotifying, nil},
		{"https://www.linkedin.com/jobs/view/inv-notified/", JobStateNotified, nil},
	}
	for _, row := range seed {
		if _, err := InsertJobIfNew(ctx, pool, sampleJob(row.url)); err != nil {
			t.Fatalf("insert %s: %v", row.url, err)
		}
		if _, err := pool.Exec(`UPDATE jobs SET state = $1, reject_reason = $2 WHERE job_url = $3`, row.state, row.reason, row.url); err != nil {
			t.Fatalf("update %s: %v", row.url, err)
		}
	}

	claimed, err := ClaimEligibleJobs(ctx, pool, 25)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %d, want 1 eligible row", len(claimed))
	}

	inv, err := LoadNotifyInventory(ctx, pool)
	if err != nil {
		t.Fatalf("LoadNotifyInventory: %v", err)
	}
	if inv.Total != 8 {
		t.Errorf("Total = %d, want 8", inv.Total)
	}
	if inv.TitleCompany != 1 || inv.Description != 1 || inv.Duplicate != 1 || inv.DetailFailed != 1 {
		t.Errorf("reject counts = %+v, want 1 each reason", inv)
	}
	if inv.Pending != 1 || inv.Eligible != 0 || inv.Notifying != 2 || inv.Notified != 1 {
		t.Errorf("state counts pending=%d eligible=%d notifying=%d notified=%d, want 1,0,2,1 (notifying now is whole-table including pre-existing plus claimed)",
			inv.Pending, inv.Eligible, inv.Notifying, inv.Notified)
	}
}

func TestSetJobsState_NotifiedAndEligibleTransitions(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/fin-a/")); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/fin-b/")); err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET state = 'eligible'`); err != nil {
		t.Fatalf("eligible: %v", err)
	}
	claimed, err := ClaimEligibleJobs(ctx, pool, 25)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed = %d, want 2", len(claimed))
	}
	ids := []int64{claimed[0].ID, claimed[1].ID}

	if err := SetJobsState(ctx, pool, ids[:1], JobStateNotified); err != nil {
		t.Fatalf("notified: %v", err)
	}
	if err := SetJobsState(ctx, pool, ids[1:], JobStateEligible); err != nil {
		t.Fatalf("eligible: %v", err)
	}

	jobs, err := ListJobsForNotify(ctx, pool)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	if jobs[0].ID != ids[0] || jobs[0].State != JobStateNotified {
		t.Errorf("job %d state = %q, want notified after HTTP 200", jobs[0].ID, jobs[0].State)
	}
	if jobs[1].ID != ids[1] || jobs[1].State != JobStateEligible {
		t.Errorf("job %d state = %q, want eligible after non-200", jobs[1].ID, jobs[1].State)
	}
}

func TestClaimWithoutFinishLeavesNotifying(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/crash-claim/")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET state = 'eligible'`); err != nil {
		t.Fatalf("eligible: %v", err)
	}
	claimed, err := ClaimEligibleJobs(ctx, pool, 25)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %d, want 1", len(claimed))
	}
	var state string
	if err := pool.QueryRow(`SELECT state FROM jobs WHERE id = $1`, claimed[0].ID).Scan(&state); err != nil {
		t.Fatalf("state: %v", err)
	}
	if state != JobStateNotifying {
		t.Errorf("state after claim-before-POST = %q, want notifying", state)
	}
}

func claimedURLs(jobs []Job) []string {
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.JobURL
	}
	return out
}

func strPtr(s string) *string { return &s }
