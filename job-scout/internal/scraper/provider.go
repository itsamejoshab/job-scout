package scraper

import (
	"context"
	"time"

	"github.com/jobscout/jobscout/internal/db"
)

// JobData is the standardized job record produced by every provider, mirroring
// the old Python JobData dataclass.
type JobData struct {
	Title         string
	Company       string
	Location      string
	JobURL        string
	Description   string
	Date          time.Time
	Source        db.JobSource
	IsRemote      bool
	SearchContext string
}

// Provider is implemented by each job-site scraper.
type Provider interface {
	Source() db.JobSource
	// ScrapeJobs runs a single search query (keys: keywords, location, f_WT).
	ScrapeJobs(ctx context.Context, query map[string]string) ([]JobData, error)
	// ScrapeHardcodedURL scrapes a pre-built URL (keys: url, description, is_remote).
	ScrapeHardcodedURL(ctx context.Context, cfg map[string]any) ([]JobData, error)
}
