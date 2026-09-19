package db

import (
	"context"
	"testing"
	"time"
)

func TestNextPendingJobID_FIFOAndSkip(t *testing.T) {
	pool := migratedPool(t)
	created := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	insert := func(url, state string, at time.Time) int64 {
		t.Helper()
		var id int64
		err := pool.QueryRow(`
			INSERT INTO jobs (title, company, location, job_url, state, created_at)
			VALUES ('title', 'company', 'remote', $1, $2, $3)
			RETURNING id
		`, url, state, at).Scan(&id)
		if err != nil {
			t.Fatalf("insert %s: %v", url, err)
		}
		return id
	}

	newer := insert("https://example.test/newer", JobStatePending, created.Add(time.Minute))
	oldest := insert("https://example.test/oldest", JobStatePending, created)
	sameTime := insert("https://example.test/same-time", JobStatePending, created)
	_ = insert("https://example.test/ready", JobStateReady, created.Add(-time.Minute))

	got, err := NextPendingJobID(context.Background(), pool, nil)
	if err != nil {
		t.Fatalf("load first pending: %v", err)
	}
	if got != oldest {
		t.Errorf("first pending id = %d, want oldest %d", got, oldest)
	}

	got, err = NextPendingJobID(context.Background(), pool, []int64{oldest})
	if err != nil {
		t.Fatalf("load pending with skip: %v", err)
	}
	if got != sameTime {
		t.Errorf("pending after skip = %d, want same-time next id %d", got, sameTime)
	}

	got, err = NextPendingJobID(context.Background(), pool, []int64{oldest, sameTime, newer})
	if err != nil {
		t.Fatalf("load exhausted pending: %v", err)
	}
	if got != 0 {
		t.Errorf("exhausted pending id = %d, want zero", got)
	}
}
