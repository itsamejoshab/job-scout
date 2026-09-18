package db

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

//go:embed seed/*.json
var seedFS embed.FS

// GetSearchSettings returns the single universal search-settings row, or nil if
// none exists yet.
func GetSearchSettings(ctx context.Context, db *sql.DB) (*SearchSettings, error) {
	var (
		s                                                SearchSettings
		inc, exc, titleIn, titleEx, companyEx, nonRemote []byte
	)
	err := db.QueryRowContext(ctx, `
		SELECT id, desc_include_words, desc_exclude_words, title_include,
		       title_exclude, company_exclude, non_remote_phrases, created_at, updated_at
		FROM search_settings ORDER BY id LIMIT 1
	`).Scan(&s.ID, &inc, &exc, &titleIn, &titleEx, &companyEx, &nonRemote, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for raw, dst := range map[*[]byte]*[]string{
		&inc: &s.DescIncludeWords, &exc: &s.DescExcludeWords, &titleIn: &s.TitleInclude,
		&titleEx: &s.TitleExclude, &companyEx: &s.CompanyExclude, &nonRemote: &s.NonRemotePhrases,
	} {
		if err := json.Unmarshal(*raw, dst); err != nil {
			return nil, err
		}
	}
	return &s, nil
}

// GetScraperSettings returns the scraper-specific row for a source, or nil.
func GetScraperSettings(ctx context.Context, db *sql.DB, source JobSource) (*ScraperSettings, error) {
	var (
		s                  ScraperSettings
		queries, hardcoded []byte
		last, next         sql.NullTime
	)
	err := db.QueryRowContext(ctx, `
		SELECT id, job_source, search_queries, hardcoded_urls, timespan_code,
		       pages_to_scrape, rounds, enabled, scrape_interval_seconds,
		       last_scraped_at, next_eligible_at, created_at, updated_at
		FROM scraper_settings WHERE job_source = $1 LIMIT 1
	`, source).Scan(&s.ID, &s.JobSource, &queries, &hardcoded, &s.TimespanCode,
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
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(queries, &s.SearchQueries); err != nil {
		return nil, err
	}
	if len(hardcoded) > 0 {
		if err := json.Unmarshal(hardcoded, &s.HardcodedURLs); err != nil {
			return nil, err
		}
	}
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

	raw, err := seedFS.ReadFile("seed/search_settings.json")
	if err != nil {
		return err
	}
	var s SearchSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("parse search_settings seed: %w", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO search_settings (desc_include_words, desc_exclude_words, title_include,
		                             title_exclude, company_exclude, non_remote_phrases)
		VALUES ($1::json, $2::json, $3::json, $4::json, $5::json, $6::json)
	`, mustJSON(s.DescIncludeWords), mustJSON(s.DescExcludeWords), mustJSON(s.TitleInclude),
		mustJSON(s.TitleExclude), mustJSON(s.CompanyExclude), mustJSON(s.NonRemotePhrases))
	if err != nil {
		return err
	}
	slog.Info("seeded universal search_settings")
	return nil
}

func seedScraperSettings(ctx context.Context, db *sql.DB) error {
	raw, err := seedFS.ReadFile("seed/scraper_settings.json")
	if err != nil {
		return err
	}
	var seeds []ScraperSettings
	if err := json.Unmarshal(raw, &seeds); err != nil {
		return fmt.Errorf("parse scraper_settings seed: %w", err)
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
			INSERT INTO scraper_settings (job_source, search_queries, hardcoded_urls,
			                              timespan_code, pages_to_scrape, rounds,
			                              enabled, scrape_interval_seconds)
			VALUES ($1, $2::json, $3::json, $4, $5, $6, $7, $8)
		`, s.JobSource, mustJSON(s.SearchQueries), mustJSON(s.HardcodedURLs),
			s.TimespanCode, s.PagesToScrape, s.Rounds, s.Enabled, intervalOrDefault(s))
		if err != nil {
			return err
		}
		slog.Info("seeded scraper_settings", "source", s.JobSource)
	}
	return nil
}

func intervalOrDefault(s ScraperSettings) int {
	if s.ScrapeIntervalSeconds > 0 {
		return s.ScrapeIntervalSeconds
	}
	return 900
}

// TryLockJobSource takes a session advisory lock for source without waiting.
func TryLockJobSource(ctx context.Context, conn *sql.Conn, source JobSource) (bool, error) {
	var ok bool
	err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, string(source)).Scan(&ok)
	return ok, err
}

// UnlockJobSource releases the session advisory lock for source.
func UnlockJobSource(ctx context.Context, conn *sql.Conn, source JobSource) error {
	_, err := conn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, string(source))
	return err
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
