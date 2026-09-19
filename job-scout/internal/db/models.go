package db

import (
	"fmt"
	"strings"
	"time"
)

// JobSource enumerates supported job sites. Values are uppercase to match the
// Postgres enum type `jobsource`.
type JobSource string

const (
	SourceLinkedIn JobSource = "LINKEDIN"
	SourceIndeed   JobSource = "INDEED"
)

// AllJobSources lists every valid source, used for validation messages.
func AllJobSources() []JobSource {
	return []JobSource{SourceLinkedIn, SourceIndeed}
}

// ParseJobSource normalizes case and validates. The old Python app was
// inconsistent (one endpoint upper(), another lower()); here it is always
// uppercased to match the enum.
func ParseJobSource(s string) (JobSource, error) {
	switch JobSource(strings.ToUpper(strings.TrimSpace(s))) {
	case SourceLinkedIn:
		return SourceLinkedIn, nil
	case SourceIndeed:
		return SourceIndeed, nil
	default:
		return "", fmt.Errorf("invalid job source: %s (valid: %v)", s, AllJobSources())
	}
}

// Job mirrors the `jobs` table.
type Job struct {
	ID             int64     `json:"id"`
	JobSource      JobSource `json:"job_source"`
	Title          string    `json:"title"`
	Company        string    `json:"company"`
	Description    *string   `json:"description"`
	Location       string    `json:"location"`
	Date           time.Time `json:"date"`
	JobURL         string    `json:"job_url"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	New            bool      `json:"new"`
	Duplicate      bool      `json:"duplicate"`
	Relevant       bool      `json:"relevant"`
	Promising      bool      `json:"promising"`
	Notified       bool      `json:"notified"`
	State          string    `json:"state"`
	RejectReason   *string   `json:"reject_reason"`
	IsRemote       bool      `json:"is_remote"`
	SearchContext  string    `json:"search_context"`
	DetailAttempts int       `json:"detail_attempts"`
	StateChangedAt time.Time `json:"state_changed_at"`
}

// SearchSettings mirrors the universal `search_settings` table.
type SearchSettings struct {
	ID               int       `json:"id"`
	DescIncludeWords []string  `json:"desc_include_words"`
	DescExcludeWords []string  `json:"desc_exclude_words"`
	TitleInclude     []string  `json:"title_include"`
	TitleExclude     []string  `json:"title_exclude"`
	CompanyExclude   []string  `json:"company_exclude"`
	NonRemotePhrases []string  `json:"non_remote_phrases"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ScraperSettings mirrors the per-source `scraper_settings` table.
type ScraperSettings struct {
	ID                    int                 `json:"id"`
	JobSource             JobSource           `json:"job_source"`
	SearchQueries         []map[string]string `json:"search_queries"`
	HardcodedURLs         []map[string]any    `json:"hardcoded_urls"`
	TimespanCode          string              `json:"timespan_code"`
	PagesToScrape         int                 `json:"pages_to_scrape"`
	Rounds                int                 `json:"rounds"`
	Enabled               bool                `json:"enabled"`
	ScrapeIntervalSeconds int                 `json:"scrape_interval_seconds"`
	LastScrapedAt         *time.Time          `json:"last_scraped_at"`
	NextEligibleAt        *time.Time          `json:"next_eligible_at"`
	CreatedAt             time.Time           `json:"created_at"`
	UpdatedAt             time.Time           `json:"updated_at"`
}
