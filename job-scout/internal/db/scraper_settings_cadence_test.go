package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jobscout/jobscout/internal/db/pgtest"
)

func TestMigrate_AddsCadenceColumnsAndUpdatesExistingRows(t *testing.T) {
	pool := pgtest.Open(t)
	applyNamedMigration(t, pool, "0001_init.sql")
	applyNamedMigration(t, pool, "0002_job_url_state.sql")

	if _, err := pool.Exec(`
		INSERT INTO scraper_settings (job_source, search_queries, global_searches, timespan_code, pages_to_scrape, rounds)
		VALUES
			('LINKEDIN', '[]', '[]', 'r84600', 1, 1),
			('INDEED', '[]', '[]', 'r86400', 1, 1)
	`); err != nil {
		t.Fatalf("insert pre-cadence scraper_settings: %v", err)
	}

	if err := Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, col := range []string{"enabled", "scrape_interval_seconds", "last_scraped_at", "next_eligible_at"} {
		if !hasScraperColumn(t, pool, col) {
			t.Errorf("scraper_settings.%s column is required", col)
		}
	}

	var (
		liEnabled  bool
		liInterval int
		liLast     sql.NullTime
		liNext     sql.NullTime
	)
	err := pool.QueryRow(`
		SELECT enabled, scrape_interval_seconds, last_scraped_at, next_eligible_at
		FROM scraper_settings WHERE job_source = 'LINKEDIN'
	`).Scan(&liEnabled, &liInterval, &liLast, &liNext)
	if err != nil {
		t.Errorf("LinkedIn cadence columns must exist so a tick can skip when not due: %v", err)
	} else {
		if !liEnabled {
			t.Error("existing LinkedIn row enabled = false, want true after migration UPDATE")
		}
		if liInterval != 900 {
			t.Errorf("existing LinkedIn scrape_interval_seconds = %d, want 900", liInterval)
		}
		if liLast.Valid {
			t.Error("last_scraped_at must be NULL until a successful scrape")
		}
		if liNext.Valid {
			t.Error("next_eligible_at must be NULL until a scrape failure")
		}
	}

	var indeedEnabled bool
	err = pool.QueryRow(`SELECT enabled FROM scraper_settings WHERE job_source = 'INDEED'`).Scan(&indeedEnabled)
	if err != nil {
		t.Errorf("Indeed enabled column must exist: %v", err)
	} else if indeedEnabled {
		t.Error("existing Indeed row enabled = true, want false after migration UPDATE")
	}
}

func TestMigrateAndSeed_LinkedInEnabledIndeedDisabled(t *testing.T) {
	pool := pgtest.Open(t)
	if err := Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	if err := SeedSettings(ctx, pool); err != nil {
		t.Fatalf("seed: %v", err)
	}

	li, err := GetScraperSettings(ctx, pool, SourceLinkedIn)
	if err != nil {
		t.Fatalf("GetScraperSettings LinkedIn: %v", err)
	}
	if li == nil {
		t.Fatal("LinkedIn scraper_settings missing after seed")
	}
	if !li.Enabled {
		t.Error("LinkedIn enabled = false, want true (cadence is migration/seed, not optional)")
	}
	if li.ScrapeIntervalSeconds != 900 {
		t.Errorf("LinkedIn scrape_interval_seconds = %d, want 900", li.ScrapeIntervalSeconds)
	}
	if li.LastScrapedAt != nil {
		t.Errorf("LinkedIn last_scraped_at = %v, want nil", li.LastScrapedAt)
	}
	if li.NextEligibleAt != nil {
		t.Errorf("LinkedIn next_eligible_at = %v, want nil", li.NextEligibleAt)
	}

	indeed, err := GetScraperSettings(ctx, pool, SourceIndeed)
	if err != nil {
		t.Fatalf("GetScraperSettings Indeed: %v", err)
	}
	if indeed == nil {
		t.Fatal("Indeed scraper_settings missing after seed")
	}
	if indeed.Enabled {
		t.Error("Indeed enabled = true, want false")
	}
}

func TestScraperSeedJSON_KeepsHelpDeskQueries(t *testing.T) {
	raw, err := seedFS.ReadFile("seed/scraper_settings.json")
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	var seeds []ScraperSettings
	if err := json.Unmarshal(raw, &seeds); err != nil {
		t.Fatalf("parse seed: %v", err)
	}

	var li *ScraperSettings
	for i := range seeds {
		if seeds[i].JobSource == SourceLinkedIn {
			li = &seeds[i]
			break
		}
	}
	if li == nil {
		t.Fatal("LinkedIn seed missing")
	}

	wantLocs := map[string]bool{
		"101076143": true,
		"102252967": true,
		"102570379": true,
		"104198807": true,
		"104779438": true,
		"105135351": true,
		"105142029": true,
		"106362955": true,
	}
	if len(li.SearchQueries) == 0 {
		t.Fatal("LinkedIn seed search_queries must stay populated")
	}
	for _, q := range li.SearchQueries {
		kw := q["keywords"]
		if kw != "IT Help Desk" && kw != "Application Support" {
			t.Errorf("LinkedIn seed keywords = %q, want IT Help Desk or Application Support (not notebook Technology geos)", kw)
		}
		if _, ok := wantLocs[q["location"]]; !ok {
			t.Errorf("LinkedIn seed geoId location %q is not in the current seed set", q["location"])
		}
		if strings.Contains(strings.ToLower(kw), "technology") {
			t.Errorf("LinkedIn seed must not switch to notebook Technology keywords, got %q", kw)
		}
	}
	if li.GlobalSearches == nil {
		t.Error("LinkedIn seed global_searches must be a list, not omitted")
	}
	wantGlobal := []string{
		"help desk or IT support jobs that are onsite near port orange, FL or hybrid if more than 10 miles, remote only if more than 40 miles",
	}
	if len(li.GlobalSearches) != len(wantGlobal) || li.GlobalSearches[0] != wantGlobal[0] {
		t.Errorf("LinkedIn seed global_searches = %#v, want %#v", li.GlobalSearches, wantGlobal)
	}
	for _, q := range li.SearchQueries {
		if _, ok := q["f_WT"]; !ok {
			t.Errorf("LinkedIn seed search_queries must include f_WT for work-type settings, got %#v", q)
		}
	}
}

func hasScraperColumn(t *testing.T, pool *sql.DB, name string) bool {
	t.Helper()
	var n int
	err := pool.QueryRow(`
		SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'scraper_settings' AND column_name = $1
	`, name).Scan(&n)
	if err != nil {
		t.Fatalf("column %s lookup: %v", name, err)
	}
	return n > 0
}
