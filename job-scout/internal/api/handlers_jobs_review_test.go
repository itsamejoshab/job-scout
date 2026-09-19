package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
)

func TestReviewJob_ReadyOnlyAndPreservesNotifiedAt(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	insert := func(url, state string, notifiedAt any) int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(`
			INSERT INTO jobs (job_source, title, company, location, job_url, state, notified_at)
			VALUES ('LINKEDIN', 'Engineer', 'Acme', 'Remote', $1, $2, $3)
			RETURNING id
		`, url, state, notifiedAt).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", state, err)
		}
		return id
	}
	notifiedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	readyID := insert("https://example.test/ready", db.JobStateReady, notifiedAt)
	pendingID := insert("https://example.test/pending", db.JobStatePending, nil)
	handler := NewServer("", &Handler{DB: pool}).Handler

	review := func(id int64, action string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"action": action})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v0/jobs/%d/review", id), bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	rec := review(readyID, db.JobStateApplied)
	if rec.Code != http.StatusOK {
		t.Fatalf("ready review status=%d body=%s", rec.Code, rec.Body.String())
	}
	var state string
	var storedNotifiedAt time.Time
	if err := pool.QueryRow(`SELECT state, notified_at FROM jobs WHERE id = $1`, readyID).Scan(&state, &storedNotifiedAt); err != nil {
		t.Fatalf("load reviewed row: %v", err)
	}
	if state != db.JobStateApplied {
		t.Errorf("state=%q, want applied", state)
	}
	if !storedNotifiedAt.Equal(notifiedAt) {
		t.Errorf("notified_at=%s, want preserved %s", storedNotifiedAt, notifiedAt)
	}
	if rec := review(readyID, db.JobStateDismissed); rec.Code != http.StatusConflict {
		t.Errorf("second review status=%d, want 409", rec.Code)
	}
	if rec := review(pendingID, db.JobStateDismissed); rec.Code != http.StatusConflict {
		t.Errorf("pending review status=%d, want 409", rec.Code)
	}
	if rec := review(999999, db.JobStateApplied); rec.Code != http.StatusNotFound {
		t.Errorf("missing review status=%d, want 404", rec.Code)
	}
}

func TestClaimReadyJobs_UsesDedupeMarkerAndTimeout(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, url := range []string{"stale", "fresh", "emailed"} {
		if _, err := pool.Exec(`
			INSERT INTO jobs (job_source, title, company, location, job_url, state)
			VALUES ('LINKEDIN', 'Engineer', 'Acme', 'Remote', $1, 'ready')
		`, "https://example.test/"+url); err != nil {
			t.Fatalf("insert %s: %v", url, err)
		}
	}
	if _, err := pool.Exec(`
		UPDATE jobs SET notify_claimed_at = now() - interval '20 minutes'
		WHERE job_url LIKE '%/stale';
		UPDATE jobs SET notify_claimed_at = now() - interval '1 minute'
		WHERE job_url LIKE '%/fresh';
		UPDATE jobs SET notified_at = now()
		WHERE job_url LIKE '%/emailed'
	`); err != nil {
		t.Fatalf("prepare markers: %v", err)
	}

	claimed, err := db.ClaimReadyJobs(t.Context(), pool, 10, 900)
	if err != nil {
		t.Fatalf("claim with timeout: %v", err)
	}
	if len(claimed) != 1 || claimed[0].JobURL != "https://example.test/stale" {
		t.Fatalf("claimed=%v, want only stale unemailed ready row", claimed)
	}
	var state string
	var notifiedAt, claimedAt time.Time
	if err := pool.QueryRow(`
		SELECT state, notified_at, notify_claimed_at FROM jobs WHERE id = $1
	`, claimed[0].ID).Scan(&state, &notifiedAt, &claimedAt); err != nil {
		t.Fatalf("load claim: %v", err)
	}
	if state != db.JobStateReady || notifiedAt.IsZero() || claimedAt.IsZero() {
		t.Errorf("claim state=%q notified_at=%s notify_claimed_at=%s", state, notifiedAt, claimedAt)
	}
	if zeroTimeout, err := db.ClaimReadyJobs(t.Context(), pool, 10, 0); err != nil {
		t.Fatalf("claim with zero timeout: %v", err)
	} else if len(zeroTimeout) != 0 {
		t.Errorf("zero timeout reclaimed rows with existing claim marker: %v", zeroTimeout)
	}
}
