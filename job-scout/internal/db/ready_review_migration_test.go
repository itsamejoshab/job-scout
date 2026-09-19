package db

import (
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/db/pgtest"
)

func TestReadyReviewMigration_BackfillsLegacyStatesAndConstrainsLifecycle(t *testing.T) {
	pool := pgtest.Open(t)
	for _, migration := range []string{
		"0001_init.sql", "0002_job_url_state.sql", "0003_scraper_cadence.sql",
		"0004_singleton_settings_indexes.sql", "0005_job_search_context.sql",
		"0006_global_searches.sql", "0007_drop_non_remote_phrases.sql",
	} {
		applyNamedMigration(t, pool, migration)
	}
	changedAt := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	for _, state := range []string{"pending", "rejected", "eligible", "notifying", "notified"} {
		if _, err := pool.Exec(`
			INSERT INTO jobs (job_source, title, company, location, job_url, state, state_changed_at)
			VALUES ('LINKEDIN', 'Engineer', 'Acme', 'Remote', $1, $2, $3)
		`, "https://example.test/"+state, state, changedAt); err != nil {
			t.Fatalf("insert %s: %v", state, err)
		}
	}

	applyNamedMigration(t, pool, "0008_ready_review.sql")

	rows, err := pool.Query(`
		SELECT job_url, state, notified_at, notify_claimed_at FROM jobs ORDER BY job_url
	`)
	if err != nil {
		t.Fatalf("load migrated jobs: %v", err)
	}
	defer rows.Close()
	seen := map[string]struct {
		state      string
		notifiedAt *time.Time
		claimedAt  *time.Time
	}{}
	for rows.Next() {
		var url, state string
		var notifiedAt, claimedAt *time.Time
		if err := rows.Scan(&url, &state, &notifiedAt, &claimedAt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen[url] = struct {
			state      string
			notifiedAt *time.Time
			claimedAt  *time.Time
		}{state, notifiedAt, claimedAt}
	}
	for _, legacy := range []string{"eligible", "notifying", "notified"} {
		got := seen["https://example.test/"+legacy]
		if got.state != JobStateReady {
			t.Errorf("%s state=%q, want ready", legacy, got.state)
		}
		if legacy == "notified" {
			if got.notifiedAt == nil || !got.notifiedAt.Equal(changedAt) {
				t.Errorf("notified notified_at=%v, want %s", got.notifiedAt, changedAt)
			}
		} else if got.notifiedAt != nil {
			t.Errorf("%s notified_at=%v, want null", legacy, got.notifiedAt)
		}
		if got.claimedAt != nil {
			t.Errorf("%s notify_claimed_at=%v, want null", legacy, got.claimedAt)
		}
	}
	for _, state := range []string{JobStateReady, JobStateApplied, JobStateDismissed} {
		if _, err := pool.Exec(`
			INSERT INTO jobs (job_source, title, company, location, job_url, state)
			VALUES ('LINKEDIN', 'Engineer', 'Acme', 'Remote', $1, $2)
		`, "https://example.test/new-"+state, state); err != nil {
			t.Errorf("new state %s rejected: %v", state, err)
		}
	}
	if _, err := pool.Exec(`
		INSERT INTO jobs (job_source, title, company, location, job_url, state)
		VALUES ('LINKEDIN', 'Engineer', 'Acme', 'Remote', 'https://example.test/legacy', 'eligible')
	`); err == nil {
		t.Error("legacy eligible state must be rejected by CHECK")
	}
}
