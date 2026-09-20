package db

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

//go:embed seed/*.json
var seedFS embed.FS

// GetSearchSettings returns the single universal search-settings row, or nil if
// none exists yet.
func GetSearchSettings(ctx context.Context, db *sql.DB) (*SearchSettings, error) {
	var (
		s                                     SearchSettings
		inc, exc, titleIn, titleEx, companyEx []byte
	)
	err := db.QueryRowContext(ctx, `
		SELECT id, desc_include_words, desc_exclude_words, title_include,
		       title_exclude, company_exclude, notifications_enabled,
		       created_at, updated_at
		FROM search_settings ORDER BY id LIMIT 1
	`).Scan(&s.ID, &inc, &exc, &titleIn, &titleEx, &companyEx,
		&s.NotificationsEnabled, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for raw, dst := range map[*[]byte]*[]string{
		&inc: &s.DescIncludeWords, &exc: &s.DescExcludeWords, &titleIn: &s.TitleInclude,
		&titleEx: &s.TitleExclude, &companyEx: &s.CompanyExclude,
	} {
		if err := json.Unmarshal(*raw, dst); err != nil {
			return nil, err
		}
	}
	return &s, nil
}

// GetNotificationsEnabled returns the saved delivery toggle. Missing settings
// default to true so existing installs keep notifications on.
func GetNotificationsEnabled(ctx context.Context, db *sql.DB) (bool, error) {
	settings, err := GetSearchSettings(ctx, db)
	if err != nil {
		return false, err
	}
	if settings == nil {
		return true, nil
	}
	return settings.NotificationsEnabled, nil
}

// SetNotificationsEnabled updates only the delivery toggle and returns the
// saved value. It creates a search_settings row from seed when none exists.
func SetNotificationsEnabled(ctx context.Context, database *sql.DB, enabled bool) (bool, error) {
	existing, err := GetSearchSettings(ctx, database)
	if err != nil {
		return false, err
	}
	if existing == nil {
		seed, err := loadSearchSettingsSeed()
		if err != nil {
			return false, err
		}
		if _, err := database.ExecContext(ctx, `
			INSERT INTO search_settings (desc_include_words, desc_exclude_words, title_include,
			                             title_exclude, company_exclude, notifications_enabled)
			VALUES ($1::json, $2::json, $3::json, $4::json, $5::json, $6)
		`, mustJSON(seed.DescIncludeWords), mustJSON(seed.DescExcludeWords),
			mustJSON(seed.TitleInclude), mustJSON(seed.TitleExclude),
			mustJSON(seed.CompanyExclude), enabled); err != nil {
			return false, err
		}
		return GetNotificationsEnabled(ctx, database)
	}
	if _, err := database.ExecContext(ctx, `
		UPDATE search_settings
		SET notifications_enabled = $1, updated_at = now()
		WHERE id = $2
	`, enabled, existing.ID); err != nil {
		return false, err
	}
	return GetNotificationsEnabled(ctx, database)
}

// ReplaceSearchSettings rewrites all five filter lists and returns the stored row.
func ReplaceSearchSettings(ctx context.Context, db *sql.DB, in SearchSettings) (*SearchSettings, error) {
	normalized := normalizeSearchSettings(in)
	existing, err := GetSearchSettings(ctx, db)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO search_settings (desc_include_words, desc_exclude_words, title_include,
			                             title_exclude, company_exclude)
			VALUES ($1::json, $2::json, $3::json, $4::json, $5::json)
		`, mustJSON(normalized.DescIncludeWords), mustJSON(normalized.DescExcludeWords),
			mustJSON(normalized.TitleInclude), mustJSON(normalized.TitleExclude),
			mustJSON(normalized.CompanyExclude)); err != nil {
			return nil, err
		}
		return GetSearchSettings(ctx, db)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE search_settings
		SET desc_include_words = $1::json,
		    desc_exclude_words = $2::json,
		    title_include = $3::json,
		    title_exclude = $4::json,
		    company_exclude = $5::json,
		    updated_at = now()
		WHERE id = $6
	`, mustJSON(normalized.DescIncludeWords), mustJSON(normalized.DescExcludeWords),
		mustJSON(normalized.TitleInclude), mustJSON(normalized.TitleExclude),
		mustJSON(normalized.CompanyExclude), existing.ID); err != nil {
		return nil, err
	}
	return GetSearchSettings(ctx, db)
}

// ResetSearchSettings restores the filter lists from embedded seed defaults.
func ResetSearchSettings(ctx context.Context, db *sql.DB) (*SearchSettings, error) {
	seed, err := loadSearchSettingsSeed()
	if err != nil {
		return nil, err
	}
	return ReplaceSearchSettings(ctx, db, seed)
}

func normalizeSearchSettings(in SearchSettings) SearchSettings {
	return SearchSettings{
		DescIncludeWords: normalizeWordList(in.DescIncludeWords),
		DescExcludeWords: normalizeWordList(in.DescExcludeWords),
		TitleInclude:     normalizeWordList(in.TitleInclude),
		TitleExclude:     normalizeWordList(in.TitleExclude),
		CompanyExclude:   normalizeWordList(in.CompanyExclude),
	}
}

func normalizeWordList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		word := strings.TrimSpace(raw)
		if word == "" {
			continue
		}
		key := strings.ToLower(word)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, word)
	}
	return out
}

func loadSearchSettingsSeed() (SearchSettings, error) {
	raw, err := seedFS.ReadFile("seed/search_settings.json")
	if err != nil {
		return SearchSettings{}, err
	}
	var s SearchSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		return SearchSettings{}, fmt.Errorf("parse search_settings seed: %w", err)
	}
	return normalizeSearchSettings(s), nil
}

// GetScraperSettings returns the scraper-specific row for a source, or nil.
func GetScraperSettings(ctx context.Context, db *sql.DB, source JobSource) (*ScraperSettings, error) {
	var (
		s               ScraperSettings
		queries, global []byte
		last, next      sql.NullTime
	)
	err := db.QueryRowContext(ctx, `
		SELECT id, job_source, search_queries, global_searches, timespan_code,
		       pages_to_scrape, rounds, enabled, scrape_interval_seconds,
		       last_scraped_at, next_eligible_at, created_at, updated_at
		FROM scraper_settings WHERE job_source = $1 LIMIT 1
	`, source).Scan(&s.ID, &s.JobSource, &queries, &global, &s.TimespanCode,
		&s.PagesToScrape, &s.Rounds, &s.Enabled, &s.ScrapeIntervalSeconds,
		&last, &next, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if last.Valid {
		t := last.Time
		s.LastScrapedAt = &t
	}
	if next.Valid {
		t := next.Time
		s.NextEligibleAt = &t
	}
	if err := json.Unmarshal(queries, &s.SearchQueries); err != nil {
		return nil, err
	}
	if len(global) > 0 {
		if err := json.Unmarshal(global, &s.GlobalSearches); err != nil {
			return nil, err
		}
	}
	if s.GlobalSearches == nil {
		s.GlobalSearches = []string{}
	}
	s.SearchQueries = normalizeSearchQueries(s.SearchQueries)
	return &s, nil
}

// AllScraperSettings returns every scraper-settings row.
func AllScraperSettings(ctx context.Context, db *sql.DB) ([]ScraperSettings, error) {
	out := []ScraperSettings{}
	for _, src := range AllJobSources() {
		s, err := GetScraperSettings(ctx, db, src)
		if err != nil {
			return nil, err
		}
		if s != nil {
			out = append(out, *s)
		}
	}
	return out, nil
}

// ReplaceScraperSettings rewrites only operator-owned provider settings.
func ReplaceScraperSettings(
	ctx context.Context,
	db *sql.DB,
	source JobSource,
	in ScraperSettings,
) (*ScraperSettings, error) {
	if _, err := db.ExecContext(ctx, `
		UPDATE scraper_settings
		SET search_queries = $1::json,
		    global_searches = $2::json,
		    timespan_code = $3,
		    pages_to_scrape = $4,
		    rounds = $5,
		    enabled = $6,
		    scrape_interval_seconds = $7,
		    updated_at = now()
		WHERE job_source = $8
	`, mustJSON(normalizeSearchQueries(in.SearchQueries)), mustJSON(normalizeGlobalSearches(in.GlobalSearches)), in.TimespanCode,
		in.PagesToScrape, in.Rounds, in.Enabled, in.ScrapeIntervalSeconds, source); err != nil {
		return nil, err
	}
	return GetScraperSettings(ctx, db, source)
}

// ResetScraperSettings restores operator-owned fields and leaves cadence intact.
func ResetScraperSettings(
	ctx context.Context,
	db *sql.DB,
	source JobSource,
) (*ScraperSettings, error) {
	seed, err := loadScraperSettingsSeed(source)
	if err != nil {
		return nil, err
	}
	if seed == nil {
		return nil, nil
	}
	return ReplaceScraperSettings(ctx, db, source, *seed)
}

// SeedSettings inserts universal + per-source settings from the embedded seed
// files if (and only if) they are missing. This fixes the original Python bug
// where scraper_settings was never seeded, so every scrape failed.
func SeedSettings(ctx context.Context, db *sql.DB) error {
	if err := seedSearchSettings(ctx, db); err != nil {
		return err
	}
	return seedScraperSettings(ctx, db)
}

func seedSearchSettings(ctx context.Context, db *sql.DB) error {
	existing, err := GetSearchSettings(ctx, db)
	if err != nil {
		return err
	}
	if existing != nil {
		slog.Info("search_settings already present, skipping seed")
		return nil
	}

	s, err := loadSearchSettingsSeed()
	if err != nil {
		return err
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO search_settings (desc_include_words, desc_exclude_words, title_include,
		                             title_exclude, company_exclude)
		VALUES ($1::json, $2::json, $3::json, $4::json, $5::json)
	`, mustJSON(s.DescIncludeWords), mustJSON(s.DescExcludeWords), mustJSON(s.TitleInclude),
		mustJSON(s.TitleExclude), mustJSON(s.CompanyExclude))
	if err != nil {
		return err
	}
	slog.Info("seeded universal search_settings")
	return nil
}

func seedScraperSettings(ctx context.Context, db *sql.DB) error {
	seeds, err := loadScraperSettingsSeeds()
	if err != nil {
		return err
	}

	for _, s := range seeds {
		existing, err := GetScraperSettings(ctx, db, s.JobSource)
		if err != nil {
			return err
		}
		if existing != nil {
			slog.Info("scraper_settings already present, skipping", "source", s.JobSource)
			continue
		}
		_, err = db.ExecContext(ctx, `
			INSERT INTO scraper_settings (job_source, search_queries, global_searches,
			                              timespan_code, pages_to_scrape, rounds,
			                              enabled, scrape_interval_seconds)
			VALUES ($1, $2::json, $3::json, $4, $5, $6, $7, $8)
		`, s.JobSource, mustJSON(normalizeSearchQueries(s.SearchQueries)), mustJSON(normalizeGlobalSearches(s.GlobalSearches)),
			s.TimespanCode, s.PagesToScrape, s.Rounds, s.Enabled, intervalOrDefault(s))
		if err != nil {
			return err
		}
		slog.Info("seeded scraper_settings", "source", s.JobSource)
	}
	return nil
}

func loadScraperSettingsSeeds() ([]ScraperSettings, error) {
	raw, err := seedFS.ReadFile("seed/scraper_settings.json")
	if err != nil {
		return nil, err
	}
	var seeds []ScraperSettings
	if err := json.Unmarshal(raw, &seeds); err != nil {
		return nil, fmt.Errorf("parse scraper_settings seed: %w", err)
	}
	return seeds, nil
}

func loadScraperSettingsSeed(source JobSource) (*ScraperSettings, error) {
	seeds, err := loadScraperSettingsSeeds()
	if err != nil {
		return nil, err
	}
	for _, s := range seeds {
		if s.JobSource == source {
			return &s, nil
		}
	}
	return nil, nil
}

func intervalOrDefault(s ScraperSettings) int {
	if s.ScrapeIntervalSeconds > 0 {
		return s.ScrapeIntervalSeconds
	}
	return 900
}

func normalizeSearchQueries(in []map[string]string) []map[string]string {
	out := make([]map[string]string, 0, len(in))
	for _, query := range in {
		out = append(out, map[string]string{
			"keywords": query["keywords"],
			"location": query["location"],
			"f_WT":     query["f_WT"],
		})
	}
	return out
}

func normalizeGlobalSearches(in []string) []string {
	if in == nil {
		return []string{}
	}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		word := strings.TrimSpace(raw)
		if word == "" {
			continue
		}
		out = append(out, word)
	}
	return out
}

// TryLockJobSource takes a session advisory lock for source without waiting.
func TryLockJobSource(ctx context.Context, conn *sql.Conn, source JobSource) (bool, error) {
	var ok bool
	err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, string(source)).Scan(&ok)
	return ok, err
}

// UnlockJobSource releases the session advisory lock for source.
func UnlockJobSource(ctx context.Context, conn *sql.Conn, source JobSource) error {
	var unlocked bool
	if err := conn.QueryRowContext(ctx,
		`SELECT pg_advisory_unlock(hashtext($1))`, string(source)).Scan(&unlocked); err != nil {
		return err
	}
	if !unlocked {
		return fmt.Errorf("advisory lock was not held for job source: %s", source)
	}
	return nil
}

// MarkScrapeSuccess sets last_scraped_at and clears next_eligible_at.
func MarkScrapeSuccess(ctx context.Context, db *sql.DB, source JobSource, at time.Time) error {
	_, err := db.ExecContext(ctx, `
		UPDATE scraper_settings
		SET last_scraped_at = $1, next_eligible_at = NULL, updated_at = now()
		WHERE job_source = $2
	`, at, source)
	return err
}

// MarkScrapeFailure sets next_eligible_at and leaves last_scraped_at unchanged.
func MarkScrapeFailure(ctx context.Context, db *sql.DB, source JobSource, nextEligible time.Time) error {
	_, err := db.ExecContext(ctx, `
		UPDATE scraper_settings
		SET next_eligible_at = $1, updated_at = now()
		WHERE job_source = $2
	`, nextEligible, source)
	return err
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
