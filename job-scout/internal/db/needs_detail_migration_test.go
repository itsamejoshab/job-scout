package db

import (
	"testing"

	"github.com/jobscout/jobscout/internal/db/pgtest"
)

func TestNeedsDetailMigration_AllowsNeedsDetailState(t *testing.T) {
	pool := pgtest.Open(t)
	for _, migration := range []string{
		"0001_init.sql", "0002_job_url_state.sql", "0003_scraper_cadence.sql",
		"0004_singleton_settings_indexes.sql", "0005_job_search_context.sql",
		"0006_global_searches.sql", "0007_drop_non_remote_phrases.sql",
		"0008_ready_review.sql",
	} {
		applyNamedMigration(t, pool, migration)
	}

	if _, err := pool.Exec(`
		INSERT INTO jobs (job_source, title, company, location, job_url, state)
		VALUES ('LINKEDIN', 'Engineer', 'Acme', 'Remote', 'https://example.test/needs-before', 'needs_detail')
	`); err == nil {
		t.Fatal("needs_detail must be rejected before migration 0009")
	}

	applyNamedMigration(t, pool, "0009_needs_detail.sql")

	if _, err := pool.Exec(`
		INSERT INTO jobs (job_source, title, company, location, job_url, state)
		VALUES ('LINKEDIN', 'Engineer', 'Acme', 'Remote', 'https://example.test/needs-after', 'needs_detail')
	`); err != nil {
		t.Fatalf("needs_detail must be accepted after migration 0009: %v", err)
	}
	for _, state := range []string{
		JobStatePending, JobStateRejected, JobStateNeedsDetail,
		JobStateReady, JobStateApplied, JobStateDismissed,
	} {
		if _, err := pool.Exec(`
			INSERT INTO jobs (job_source, title, company, location, job_url, state)
			VALUES ('LINKEDIN', 'Engineer', 'Acme', 'Remote', $1, $2)
		`, "https://example.test/state-"+state, state); err != nil {
			t.Errorf("state %s rejected: %v", state, err)
		}
	}
	if _, err := pool.Exec(`
		INSERT INTO jobs (job_source, title, company, location, job_url, state)
		VALUES ('LINKEDIN', 'Engineer', 'Acme', 'Remote', 'https://example.test/bogus', 'bogus')
	`); err == nil {
		t.Error("bogus state must still be rejected by CHECK")
	}
}
