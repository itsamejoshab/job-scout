package db

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jobscout/jobscout/internal/db/pgtest"
)

func TestMigrate_EnforcesSingletonSettingsAndAddsQueryIndexes(t *testing.T) {
	pool := pgtest.Open(t)
	applyNamedMigration(t, pool, "0001_init.sql")
	applyNamedMigration(t, pool, "0002_job_url_state.sql")
	applyNamedMigration(t, pool, "0003_scraper_cadence.sql")

	if _, err := pool.Exec(`
		INSERT INTO scraper_settings
			(job_source, search_queries, hardcoded_urls, timespan_code, pages_to_scrape, rounds)
		VALUES
			('LINKEDIN', '[]', '[]', 'r86400', 1, 1),
			('LINKEDIN', '[]', '[]', 'r86400', 2, 2),
			('INDEED', '[]', '[]', 'r86400', 3, 3),
			(NULL, '[]', '[]', 'r86400', 4, 4)
	`); err != nil {
		t.Fatalf("insert dirty scraper_settings: %v", err)
	}
	if _, err := pool.Exec(`
		INSERT INTO search_settings
			(desc_include_words, desc_exclude_words, title_include, title_exclude,
			 company_exclude, non_remote_phrases)
		VALUES
			('["keep"]', '[]', '[]', '[]', '[]', '[]'),
			('["drop"]', '[]', '[]', '[]', '[]', '[]')
	`); err != nil {
		t.Fatalf("insert duplicate search_settings: %v", err)
	}

	var (
		linkedInKeepID int64
		indeedID       int64
		nullSourceID   int64
		searchKeepID   int64
	)
	if err := pool.QueryRow(`SELECT MIN(id) FROM scraper_settings WHERE job_source = 'LINKEDIN'`).Scan(&linkedInKeepID); err != nil {
		t.Fatalf("lowest LinkedIn settings id: %v", err)
	}
	if err := pool.QueryRow(`SELECT id FROM scraper_settings WHERE job_source = 'INDEED'`).Scan(&indeedID); err != nil {
		t.Fatalf("Indeed settings id: %v", err)
	}
	if err := pool.QueryRow(`SELECT id FROM scraper_settings WHERE job_source IS NULL`).Scan(&nullSourceID); err != nil {
		t.Fatalf("null-source settings id: %v", err)
	}
	if err := pool.QueryRow(`SELECT MIN(id) FROM search_settings`).Scan(&searchKeepID); err != nil {
		t.Fatalf("lowest search settings id: %v", err)
	}

	if err := Migrate(pool); err != nil {
		t.Fatalf("migrate dirty settings database: %v", err)
	}

	assertSingleKeptID(t, pool, "scraper_settings", "job_source = 'LINKEDIN'", linkedInKeepID)
	assertSingleKeptID(t, pool, "scraper_settings", "job_source = 'INDEED'", indeedID)
	assertSingleKeptID(t, pool, "search_settings", "TRUE", searchKeepID)

	var nullRowExists bool
	if err := pool.QueryRow(`SELECT EXISTS(SELECT 1 FROM scraper_settings WHERE id = $1)`, nullSourceID).Scan(&nullRowExists); err != nil {
		t.Fatalf("check null-source row removal: %v", err)
	} else if nullRowExists {
		t.Errorf("null-source scraper_settings id %d still exists, want row deleted", nullSourceID)
	}

	var nullable string
	if err := pool.QueryRow(`
		SELECT is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name = 'scraper_settings'
		  AND column_name = 'job_source'
	`).Scan(&nullable); err != nil {
		t.Fatalf("read scraper_settings.job_source nullability: %v", err)
	} else if nullable != "NO" {
		t.Errorf("scraper_settings.job_source is_nullable = %q, want NO", nullable)
	}

	_, err := pool.Exec(`
		INSERT INTO scraper_settings
			(job_source, search_queries, hardcoded_urls, timespan_code, pages_to_scrape, rounds)
		VALUES ('LINKEDIN', '[]', '[]', 'r86400', 1, 1)
	`)
	assertPostgresCode(t, err, "23505", "duplicate scraper_settings job_source")

	_, err = pool.Exec(`
		INSERT INTO scraper_settings
			(job_source, search_queries, hardcoded_urls, timespan_code, pages_to_scrape, rounds)
		VALUES (NULL, '[]', '[]', 'r86400', 1, 1)
	`)
	assertPostgresCode(t, err, "23502", "null scraper_settings job_source")

	_, err = pool.Exec(`
		INSERT INTO search_settings
			(desc_include_words, desc_exclude_words, title_include, title_exclude,
			 company_exclude, non_remote_phrases)
		VALUES ('[]', '[]', '[]', '[]', '[]', '[]')
	`)
	assertPostgresCode(t, err, "23505", "second search_settings row")

	if !hasExactIndexKeys(t, pool, "jobs", "job_source,state") {
		t.Error("jobs needs an index on (job_source, state)")
	}
	if !hasExactIndexKeys(t, pool, "jobs", "created_at") {
		t.Error("jobs needs an index on created_at")
	}
}

func assertSingleKeptID(t *testing.T, pool *sql.DB, table, predicate string, wantID int64) {
	t.Helper()
	var (
		count int
		gotID int64
	)
	query := "SELECT COUNT(*), MIN(id) FROM " + table + " WHERE " + predicate
	if err := pool.QueryRow(query).Scan(&count, &gotID); err != nil {
		t.Fatalf("inspect %s retained row: %v", table, err)
	}
	if count != 1 {
		t.Errorf("%s retained rows = %d, want 1", table, count)
	}
	if gotID != wantID {
		t.Errorf("%s retained id = %d, want lowest id %d", table, gotID, wantID)
	}
}

func assertPostgresCode(t *testing.T, err error, wantCode, operation string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s succeeded, want PostgreSQL error %s", operation, wantCode)
		return
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Errorf("%s error type = %T, want *pgconn.PgError: %v", operation, err, err)
		return
	}
	if pgErr.Code != wantCode {
		t.Errorf("%s PostgreSQL code = %s, want %s: %v", operation, pgErr.Code, wantCode, err)
	}
}

func hasExactIndexKeys(t *testing.T, pool *sql.DB, table, keys string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(`
		SELECT EXISTS (
			SELECT 1
			FROM pg_index AS i
			JOIN pg_class AS tbl ON tbl.oid = i.indrelid
			JOIN pg_namespace AS ns ON ns.oid = tbl.relnamespace
			WHERE ns.nspname = 'public'
			  AND tbl.relname = $1
			  AND i.indpred IS NULL
			  AND i.indexprs IS NULL
			  AND (
				SELECT string_agg(pg_get_indexdef(i.indexrelid, position, TRUE), ',' ORDER BY position)
				FROM generate_series(1, i.indnkeyatts) AS position
			  ) = $2
		)
	`, table, keys).Scan(&exists)
	if err != nil {
		t.Fatalf("index lookup for %s(%s): %v", table, keys, err)
	}
	return exists
}
