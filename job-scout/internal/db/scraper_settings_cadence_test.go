package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"reflect"
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

	var sources []JobSource
	for _, src := range AllJobSources() {
		sources = append(sources, src)
	}
	if !containsSource(sources, "DICE") {
		t.Errorf("AllJobSources = %v, want DICE", sources)
	}
	parsed, err := ParseJobSource("dice")
	if err != nil {
		t.Fatalf("ParseJobSource DICE: %v", err)
	}
	if parsed != "DICE" {
		t.Errorf("ParseJobSource dice = %q, want DICE", parsed)
	}

	dice, err := GetScraperSettings(ctx, pool, "DICE")
	if err != nil {
		t.Fatalf("GetScraperSettings Dice: %v", err)
	}
	if dice == nil {
		t.Fatal("Dice scraper_settings missing after seed")
	}
	if !dice.Enabled {
		t.Error("Dice enabled = false, want true in seed JSON")
	}
	if dice.ScrapeIntervalSeconds != 10800 {
		t.Errorf("Dice scrape_interval_seconds = %d, want 10800", dice.ScrapeIntervalSeconds)
	}
	if dice.TimespanCode != "24h" || dice.PagesToScrape != 1 || dice.Rounds != 1 {
		t.Errorf("Dice seed timespan/pages/rounds = %#v", dice)
	}
	if !reflect.DeepEqual(dice.GlobalSearches, []string{}) {
		t.Errorf("Dice global_searches = %#v, want empty list", dice.GlobalSearches)
	}
	if len(dice.SearchQueries) != 1 ||
		dice.SearchQueries[0]["keywords"] != "Desktop or Endpoint or Application Support" ||
		dice.SearchQueries[0]["location"] != "Port Orange, FL" ||
		dice.SearchQueries[0]["include_remote"] != "true" {
		t.Errorf("Dice seed search_queries = %#v", dice.SearchQueries)
	}
	if _, ok := dice.SearchQueries[0]["f_WT"]; ok {
		t.Errorf("Dice seed must not persist f_WT: %#v", dice.SearchQueries[0])
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
	if len(li.GlobalSearches) != 0 {
		t.Errorf("LinkedIn seed global_searches = %#v, want empty", li.GlobalSearches)
	}
	for _, q := range li.SearchQueries {
		if _, ok := q["f_WT"]; !ok {
			t.Errorf("LinkedIn seed search_queries must include f_WT for work-type settings, got %#v", q)
		}
	}
}

func TestScraperSeedJSON_DiceDefaults(t *testing.T) {
	raw, err := seedFS.ReadFile("seed/scraper_settings.json")
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	var seeds []ScraperSettings
	if err := json.Unmarshal(raw, &seeds); err != nil {
		t.Fatalf("parse seed: %v", err)
	}

	var dice *ScraperSettings
	for i := range seeds {
		if seeds[i].JobSource == "DICE" {
			dice = &seeds[i]
			break
		}
	}
	if dice == nil {
		t.Fatal("Dice seed missing")
	}
	if !dice.Enabled {
		t.Error("Dice seed enabled = false, want true")
	}
	if dice.ScrapeIntervalSeconds != 10800 {
		t.Errorf("Dice seed scrape_interval_seconds = %d, want 10800", dice.ScrapeIntervalSeconds)
	}
	if dice.TimespanCode != "24h" || dice.PagesToScrape != 1 || dice.Rounds != 1 {
		t.Errorf("Dice seed timespan/pages/rounds = %#v", dice)
	}
	if dice.GlobalSearches == nil {
		t.Error("Dice seed global_searches must be a list, not omitted")
	}
	if len(dice.GlobalSearches) != 0 {
		t.Errorf("Dice seed global_searches = %#v, want empty", dice.GlobalSearches)
	}
	if len(dice.SearchQueries) != 1 {
		t.Fatalf("Dice seed search_queries len = %d, want 1 expanded pair", len(dice.SearchQueries))
	}
	query := dice.SearchQueries[0]
	if query["keywords"] != "Desktop or Endpoint or Application Support" ||
		query["location"] != "Port Orange, FL" ||
		query["include_remote"] != "true" {
		t.Errorf("Dice seed query = %#v", query)
	}
	if _, ok := query["f_WT"]; ok {
		t.Errorf("Dice seed must omit f_WT: %#v", query)
	}
}

func TestMigrate_AddsDiceEnumWithoutInsertingScraperRow(t *testing.T) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	found := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		text := string(body)
		if !strings.Contains(text, "ADD VALUE 'DICE'") {
			continue
		}
		found++
		if strings.Contains(strings.ToUpper(text), "INSERT") {
			t.Errorf("%s must not insert scraper rows", entry.Name())
		}
		executable := stripSQLComments(text)
		collapsed := strings.Join(strings.Fields(executable), " ")
		collapsed = strings.TrimSuffix(collapsed, ";")
		if collapsed != "ALTER TYPE jobsource ADD VALUE 'DICE'" {
			t.Errorf("%s must execute ALTER TYPE jobsource ADD VALUE 'DICE' only, got %q", entry.Name(), executable)
		}
	}
	if found != 1 {
		t.Fatalf("want exactly one Dice enum migration, found %d", found)
	}

	pool := pgtest.Open(t)
	for _, migration := range []string{
		"0001_init.sql", "0002_job_url_state.sql", "0003_scraper_cadence.sql",
		"0004_singleton_settings_indexes.sql", "0005_job_search_context.sql",
		"0006_global_searches.sql", "0007_drop_non_remote_phrases.sql",
		"0008_ready_review.sql", "0009_needs_detail.sql",
		"0010_notifications_enabled.sql",
	} {
		applyNamedMigration(t, pool, migration)
	}
	if _, err := pool.Exec(`
		INSERT INTO scraper_settings (job_source, search_queries, global_searches, timespan_code, pages_to_scrape, rounds)
		VALUES
			('LINKEDIN', '[]', '[]', 'r86400', 1, 1),
			('INDEED', '[]', '[]', 'r86400', 1, 1)
	`); err != nil {
		t.Fatalf("insert pre-dice scraper_settings: %v", err)
	}

	if err := Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var labels []string
	rows, err := pool.Query(`
		SELECT e.enumlabel
		FROM pg_enum e
		JOIN pg_type t ON e.enumtypid = t.oid
		WHERE t.typname = 'jobsource'
		ORDER BY e.enumlabel
	`)
	if err != nil {
		t.Fatalf("list jobsource enum: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			t.Fatalf("scan enum label: %v", err)
		}
		labels = append(labels, label)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("enum rows: %v", err)
	}
	if !containsString(labels, "DICE") {
		t.Errorf("jobsource enum = %v, want DICE", labels)
	}

	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM scraper_settings`).Scan(&n); err != nil {
		t.Fatalf("count scraper_settings: %v", err)
	}
	if n != 2 {
		t.Errorf("scraper_settings rows = %d, want 2 (migration must not insert DICE)", n)
	}
}

func TestSeedSettings_InsertsMissingDiceWithoutChangingExistingRows(t *testing.T) {
	pool := pgtest.Open(t)
	if err := Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(`
		INSERT INTO scraper_settings (
			job_source, search_queries, global_searches, timespan_code,
			pages_to_scrape, rounds, enabled, scrape_interval_seconds
		) VALUES
			('LINKEDIN', '[]', '[]', 'kept-li', 77, 3, false, 123),
			('INDEED', '[]', '[]', 'kept-in', 44, 2, true, 456)
	`); err != nil {
		t.Fatalf("insert existing providers: %v", err)
	}

	if err := SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}

	li, err := GetScraperSettings(t.Context(), pool, SourceLinkedIn)
	if err != nil || li == nil {
		t.Fatalf("LinkedIn after seed: %v %#v", err, li)
	}
	if li.TimespanCode != "kept-li" || li.PagesToScrape != 77 || li.Rounds != 3 ||
		li.Enabled || li.ScrapeIntervalSeconds != 123 {
		t.Errorf("LinkedIn row changed by seed: %#v", li)
	}
	indeed, err := GetScraperSettings(t.Context(), pool, SourceIndeed)
	if err != nil || indeed == nil {
		t.Fatalf("Indeed after seed: %v %#v", err, indeed)
	}
	if indeed.TimespanCode != "kept-in" || indeed.PagesToScrape != 44 || indeed.Rounds != 2 ||
		!indeed.Enabled || indeed.ScrapeIntervalSeconds != 456 {
		t.Errorf("Indeed row changed by seed: %#v", indeed)
	}

	var found bool
	all, err := AllScraperSettings(t.Context(), pool)
	if err != nil {
		t.Fatalf("AllScraperSettings: %v", err)
	}
	for i := range all {
		if all[i].JobSource == "DICE" {
			found = true
			dice := all[i]
			if !dice.Enabled || dice.ScrapeIntervalSeconds != 10800 || dice.TimespanCode != "24h" ||
				dice.PagesToScrape != 1 || dice.Rounds != 1 {
				t.Errorf("seeded Dice cadence fields: %#v", dice)
			}
			if !reflect.DeepEqual(dice.GlobalSearches, []string{}) {
				t.Errorf("seeded Dice global_searches=%#v", dice.GlobalSearches)
			}
			if len(dice.SearchQueries) != 1 ||
				dice.SearchQueries[0]["keywords"] != "Desktop or Endpoint or Application Support" ||
				dice.SearchQueries[0]["location"] != "Port Orange, FL" ||
				dice.SearchQueries[0]["include_remote"] != "true" {
				t.Errorf("seeded Dice search_queries=%#v", dice.SearchQueries)
			}
		}
	}
	if !found {
		t.Fatal("seed must insert missing Dice row")
	}
}

func TestNormalizeSearchQueries_KeepsIncludeRemoteAndWorkType(t *testing.T) {
	pool := pgtest.Open(t)
	if err := Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}

	stored, err := ReplaceScraperSettings(t.Context(), pool, SourceLinkedIn, ScraperSettings{
		Enabled:               true,
		ScrapeIntervalSeconds: 900,
		TimespanCode:          "r86400",
		PagesToScrape:         1,
		Rounds:                1,
		SearchQueries: []map[string]string{{
			"keywords":       "Support",
			"location":       "101076143",
			"f_WT":           "2",
			"include_remote": "true",
			"dropped":        "nope",
		}},
		GlobalSearches: []string{},
	})
	if err != nil || stored == nil {
		t.Fatalf("replace LinkedIn: %v %#v", err, stored)
	}
	got := stored.SearchQueries[0]
	if got["keywords"] != "Support" || got["location"] != "101076143" ||
		got["f_WT"] != "2" || got["include_remote"] != "true" {
		t.Fatalf("normalize must keep keywords, location, f_WT, include_remote: %#v", got)
	}
	if _, ok := got["dropped"]; ok {
		t.Errorf("normalize must drop unknown keys: %#v", got)
	}

	dice, err := ReplaceScraperSettings(t.Context(), pool, "DICE", ScraperSettings{
		Enabled:               true,
		ScrapeIntervalSeconds: 10800,
		TimespanCode:          "24h",
		PagesToScrape:         1,
		Rounds:                1,
		SearchQueries: []map[string]string{{
			"keywords":       "Desktop",
			"location":       "Port Orange, FL",
			"include_remote": "false",
			"f_WT":           "2",
		}},
		GlobalSearches: []string{},
	})
	if err != nil {
		t.Fatalf("replace Dice: %v", err)
	}
	if dice == nil {
		t.Fatal("Dice row missing for normalize round-trip")
	}
	if dice.SearchQueries[0]["include_remote"] != "false" {
		t.Errorf("Dice include_remote=%q, want false", dice.SearchQueries[0]["include_remote"])
	}
	if _, ok := dice.SearchQueries[0]["f_WT"]; ok {
		t.Errorf("Dice must not persist f_WT: %#v", dice.SearchQueries[0])
	}
}

func stripSQLComments(text string) string {
	var b strings.Builder
	lines := strings.Split(text, "\n")
	inBlock := false
	for _, line := range lines {
		rest := line
		if inBlock {
			end := strings.Index(rest, "*/")
			if end < 0 {
				continue
			}
			rest = rest[end+2:]
			inBlock = false
		}
		for {
			start := strings.Index(rest, "/*")
			comment := strings.Index(rest, "--")
			if start >= 0 && (comment < 0 || start < comment) {
				end := strings.Index(rest[start+2:], "*/")
				if end < 0 {
					b.WriteString(rest[:start])
					inBlock = true
					rest = ""
					break
				}
				rest = rest[:start] + rest[start+2+end+2:]
				continue
			}
			if comment >= 0 {
				rest = rest[:comment]
			}
			break
		}
		if strings.TrimSpace(rest) != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(rest)
		}
	}
	return b.String()
}

func containsSource(sources []JobSource, want JobSource) bool {
	for _, src := range sources {
		if src == want {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
