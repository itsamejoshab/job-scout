package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
	"github.com/jobscout/jobscout/internal/domain"
)

type jobRow struct {
	State          string
	RejectReason   *string
	DetailAttempts int
	StateChangedAt time.Time
}

func (row jobRow) String() string {
	reason := "<nil>"
	if row.RejectReason != nil {
		reason = *row.RejectReason
	}
	return fmt.Sprintf("state=%s reject_reason=%s detail_attempts=%d state_changed_at=%s",
		row.State, reason, row.DetailAttempts, row.StateChangedAt.UTC())
}

func (row jobRow) equals(other jobRow) bool {
	switch {
	case row.State != other.State,
		row.DetailAttempts != other.DetailAttempts,
		!row.StateChangedAt.Equal(other.StateChangedAt),
		(row.RejectReason == nil) != (other.RejectReason == nil):
		return false
	case row.RejectReason != nil && *row.RejectReason != *other.RejectReason:
		return false
	}
	return true
}

func jobRowsByURL(t *testing.T, pool *sql.DB) map[string]jobRow {
	t.Helper()
	rows, err := pool.Query(
		`SELECT job_url, state, reject_reason, detail_attempts, state_changed_at FROM jobs`)
	if err != nil {
		t.Fatalf("read jobs: %v", err)
	}
	defer rows.Close()

	byURL := map[string]jobRow{}
	for rows.Next() {
		var url string
		var row jobRow
		if err := rows.Scan(
			&url, &row.State, &row.RejectReason, &row.DetailAttempts, &row.StateChangedAt,
		); err != nil {
			t.Fatalf("scan job: %v", err)
		}
		byURL[url] = row
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate jobs: %v", err)
	}
	return byURL
}

func TestReEvaluateRejectedJobs_ReturnsUpdatedCountAndOnlyResetsRejected(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := t.Context()
	for _, job := range []db.Job{
		{JobSource: db.SourceLinkedIn, Title: "Detail Failed", Company: "Acme",
			Location: "Remote", JobURL: "https://example.test/jobs/detail-failed"},
		{JobSource: db.SourceLinkedIn, Title: "Title Rejected", Company: "Acme",
			Location: "Remote", JobURL: "https://example.test/jobs/title-rejected"},
		{JobSource: db.SourceLinkedIn, Title: "Eligible", Company: "Acme",
			Location: "Remote", JobURL: "https://example.test/jobs/eligible"},
		{JobSource: db.SourceLinkedIn, Title: "Notified", Company: "Acme",
			Location: "Remote", JobURL: "https://example.test/jobs/notified"},
		{JobSource: db.SourceLinkedIn, Title: "Pending", Company: "Acme",
			Location: "Remote", JobURL: "https://example.test/jobs/pending"},
		{JobSource: db.SourceLinkedIn, Title: "Notifying", Company: "Acme",
			Location: "Remote", JobURL: "https://example.test/jobs/notifying"},
	} {
		if _, err := db.InsertJobIfNew(ctx, pool, job); err != nil {
			t.Fatalf("insert %s: %v", job.JobURL, err)
		}
	}
	if _, err := pool.Exec(`
		UPDATE jobs SET state = 'rejected', reject_reason = 'detail_failed', detail_attempts = 3,
		                state_changed_at = now() - interval '1 hour'
		WHERE job_url = 'https://example.test/jobs/detail-failed';
		UPDATE jobs SET state = 'rejected', reject_reason = 'title_company', detail_attempts = 1,
		                state_changed_at = now() - interval '1 hour'
		WHERE job_url = 'https://example.test/jobs/title-rejected';
		UPDATE jobs SET state = 'ready', reject_reason = NULL, detail_attempts = 2,
		                state_changed_at = now() - interval '1 hour'
		WHERE job_url = 'https://example.test/jobs/eligible';
		UPDATE jobs SET state = 'applied', notified = TRUE, reject_reason = NULL, detail_attempts = 2,
		                state_changed_at = now() - interval '1 hour'
		WHERE job_url = 'https://example.test/jobs/notified';
		UPDATE jobs SET state = 'pending', reject_reason = NULL, detail_attempts = 1,
		                state_changed_at = now() - interval '1 hour'
		WHERE job_url = 'https://example.test/jobs/pending';
		UPDATE jobs SET state = 'dismissed', reject_reason = NULL, detail_attempts = 2,
		                state_changed_at = now() - interval '1 hour'
		WHERE job_url = 'https://example.test/jobs/notifying';
	`); err != nil {
		t.Fatalf("prepare jobs: %v", err)
	}
	before := jobRowsByURL(t, pool)

	h := &Handler{DB: pool}
	req := httptest.NewRequest(http.MethodPost, "/api/v0/jobs/re-evaluate", nil)
	rec := httptest.NewRecorder()
	NewServer("", h).Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v0/jobs/re-evaluate status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Updated *int `json:"updated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("re-evaluate JSON: %v", err)
	}
	if body.Updated == nil || *body.Updated != 2 {
		t.Fatalf("updated = %v, want 2 rejected rows", body.Updated)
	}

	after := jobRowsByURL(t, pool)
	for _, url := range []string{
		"https://example.test/jobs/detail-failed",
		"https://example.test/jobs/title-rejected",
	} {
		row := after[url]
		if row.State != db.JobStatePending {
			t.Errorf("%s state = %q, want pending", url, row.State)
		}
		if row.RejectReason != nil {
			t.Errorf("%s reject_reason = %q, want cleared", url, *row.RejectReason)
		}
		if row.DetailAttempts != 0 {
			t.Errorf("%s detail_attempts = %d, want 0", url, row.DetailAttempts)
		}
		if row.DetailAttempts >= domain.MaxDetailAttempts {
			t.Errorf("%s detail_attempts = %d, want below the %d attempt ceiling so notify can fetch again",
				url, row.DetailAttempts, domain.MaxDetailAttempts)
		}
		if !row.StateChangedAt.After(before[url].StateChangedAt) {
			t.Errorf("%s state_changed_at = %s, want later than %s",
				url, row.StateChangedAt, before[url].StateChangedAt)
		}
	}

	for _, url := range []string{
		"https://example.test/jobs/eligible",
		"https://example.test/jobs/notified",
		"https://example.test/jobs/pending",
		"https://example.test/jobs/notifying",
	} {
		if !after[url].equals(before[url]) {
			t.Errorf("%s = %s, want unchanged %s", url, after[url], before[url])
		}
	}

	rec = httptest.NewRecorder()
	NewServer("", h).Handler.ServeHTTP(
		rec, httptest.NewRequest(http.MethodPost, "/api/v0/jobs/re-evaluate", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("second POST status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("second re-evaluate JSON: %v", err)
	}
	if body.Updated == nil || *body.Updated != 0 {
		t.Errorf("updated with no rejected rows = %v, want 0", body.Updated)
	}
	settled := jobRowsByURL(t, pool)
	for url, row := range after {
		if !settled[url].equals(row) {
			t.Errorf("%s changed on a no-op run: %s, want %s", url, settled[url], row)
		}
	}
}
