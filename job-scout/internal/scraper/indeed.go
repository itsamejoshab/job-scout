package scraper

import (
	"context"

	"github.com/jobscout/jobscout/internal/db"
)

// IndeedScraper is a placeholder, mirroring the not-yet-implemented Python one.
type IndeedScraper struct{}

func NewIndeed(_ db.ScraperSettings) *IndeedScraper { return &IndeedScraper{} }

func (s *IndeedScraper) Source() db.JobSource { return db.SourceIndeed }

func (s *IndeedScraper) ScrapeJobs(_ context.Context, _ map[string]string) ([]JobData, error) {
	return nil, nil
}

func (s *IndeedScraper) ScrapeHardcodedURL(_ context.Context, _ map[string]any) ([]JobData, error) {
	return nil, nil
}
