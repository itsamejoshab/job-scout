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
	IsRemote         bool
	SearchContext    string
	SearchIntention  string
}

// Provider is implemented by each job-site scraper.
type Provider interface {
	Source() db.JobSource
	// ScrapeJobs runs a single search query (keys: keywords, optional location, optional f_WT).
	// For LinkedIn, f_WT selects work types that are prepended to keywords;
	// location is sent as geoId. Global searches omit location.
	ScrapeJobs(ctx context.Context, query map[string]string) ([]JobData, error)
}
