package db

import (
	"context"
	"database/sql"
	"testing"
)

func TestListJobsForNotify_ReturnsWholeTableIncludingReviewedRows(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/notify-a/")); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/notify-b/")); err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET state = 'applied', notified_at = now() WHERE job_url LIKE '%notify-a/'`); err != nil {
		t.Fatalf("mark applied: %v", err)
	}

	jobs, err := ListJobsForNotify(ctx, pool)
	if err != nil {
		t.Fatalf("ListJobsForNotify: %v", err)
	}
	if len(jobs) != 2 {
		t.Errorf("ListJobsForNotify len=%d, want 2 (whole table including reviewed rows, for duplicate check)", len(jobs))
	}
	states := map[string]int{}
	for _, j := range jobs {
		states[j.State]++
	}
	if states[JobStateApplied] != 1 || states[JobStatePending] != 1 {
		t.Errorf("ListJobsForNotify by state = %v, want one applied and one pending", states)
	}
}

func TestUpdateJobNotifyState_MarksReadyAndKeepsRow(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/notify-keep/")); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var id int64
	if err := pool.QueryRow(`SELECT id FROM jobs`).Scan(&id); err != nil {
		t.Fatalf("id: %v", err)
	}
	desc := "computer windows"
	if err := UpdateJobNotifyState(ctx, pool, id, JobStateReady, nil, &desc, 0); err != nil {
		t.Fatalf("UpdateJobNotifyState: %v", err)
	}

	var (
		n        int
		state    string
		reason   sql.NullString
		body     sql.NullString
		attempts int
	)
	if err := pool.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1 (filter must not delete)", n)
	}
	if err := pool.QueryRow(`SELECT state, reject_reason, description, detail_attempts FROM jobs WHERE id = $1`, id).
		Scan(&state, &reason, &body, &attempts); err != nil {
		t.Fatalf("load: %v", err)
	}
	if state != JobStateReady {
		t.Errorf("state = %q, want ready", state)
	}
	if reason.Valid {
		t.Errorf("reject_reason = %q, want nil", reason.String)
	}
	if !body.Valid || body.String != desc {
		t.Errorf("description = %v, want %q", body, desc)
	}
	if attempts != 0 {
		t.Errorf("detail_attempts = %d, want 0", attempts)
	}
}

func TestUpdateJobNotifyState_PersistsDetailAttemptsWhilePending(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/notify-attempts/")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var id int64
	if err := pool.QueryRow(`SELECT id FROM jobs`).Scan(&id); err != nil {
		t.Fatalf("id: %v", err)
	}
	if err := UpdateJobNotifyState(ctx, pool, id, JobStatePending, nil, nil, 2); err != nil {
		t.Fatalf("UpdateJobNotifyState attempts: %v", err)
	}
	var (
		state    string
		attempts int
	)
	if err := pool.QueryRow(`SELECT state, detail_attempts FROM jobs WHERE id = $1`, id).Scan(&state, &attempts); err != nil {
		t.Fatalf("load: %v", err)
	}
	if state != JobStatePending {
		t.Errorf("state = %q, want pending", state)
	}
	if attempts != 2 {
		t.Errorf("detail_attempts = %d, want 2", attempts)
	}
}

func TestUpdateJobNotifyState_DuplicateRejectKeepsLoserRow(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/dup-1/")); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	j2 := sampleJob("https://www.linkedin.com/jobs/view/dup-2/")
	if _, err := InsertJobIfNew(ctx, pool, j2); err != nil {
		t.Fatalf("insert 2: %v", err)
	}

	var loserID int64
	if err := pool.QueryRow(`SELECT id FROM jobs WHERE job_url LIKE '%dup-2/'`).Scan(&loserID); err != nil {
		t.Fatalf("loser id: %v", err)
	}
	reason := "duplicate"
	if err := UpdateJobNotifyState(ctx, pool, loserID, JobStateRejected, &reason, nil, 0); err != nil {
		t.Fatalf("reject loser: %v", err)
	}

	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("rows = %d, want 2 (duplicate losers are kept)", n)
	}
	var state string
	var gotReason sql.NullString
	if err := pool.QueryRow(`SELECT state, reject_reason FROM jobs WHERE id = $1`, loserID).Scan(&state, &gotReason); err != nil {
		t.Fatalf("loser row: %v", err)
	}
	if state != JobStateRejected || !gotReason.Valid || gotReason.String != "duplicate" {
		t.Errorf("loser state=%q reason=%v, want rejected/duplicate", state, gotReason)
	}
}
