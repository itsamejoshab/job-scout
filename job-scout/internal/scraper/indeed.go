package scraper

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jobscout/jobscout/internal/db"
)

type indeedActorInput struct {
	Query              string  `json:"query"`
	Country            string  `json:"country"`
	Location           string  `json:"location,omitempty"`
	Radius             string  `json:"radius,omitempty"`
	JobType            string  `json:"jobType,omitempty"`
	FromDays           string  `json:"fromDays,omitempty"`
	Remote             *string `json:"remote,omitempty"`
	MaxRows            int     `json:"maxRows,omitempty"`
	EnableUniqueJobs   bool    `json:"enableUniqueJobs"`
	IncludeSimilarJobs bool    `json:"includeSimilarJobs"`
}

// IndeedScraper starts one or more Apify Indeed actor runs per search query.
type IndeedScraper struct {
	settings    db.ScraperSettings
	options     db.IndeedProviderOptions
	client      *ApifyClient
	budgetCents int
	AroundStart func(ctx context.Context, fn func() error) error
}

func NewIndeed(settings db.ScraperSettings, client *ApifyClient, budgetCents int) *IndeedScraper {
	opts, err := db.ParseIndeedOptions(settings.ProviderOptions)
	if err != nil || opts.Country == "" {
		opts = db.DefaultIndeedOptions()
	}
	if opts.MaxRows < 1 {
		opts.MaxRows = 100
	}
	return &IndeedScraper{
		settings:    settings,
		options:     opts,
		client:      client,
		budgetCents: budgetCents,
	}
}

func (s *IndeedScraper) Source() db.JobSource { return db.SourceIndeed }

func (s *IndeedScraper) ScrapeJobs(ctx context.Context, query map[string]string) ([]JobData, error) {
	if s.client == nil {
		return nil, fmt.Errorf("apify client is required")
	}
	var all []JobData
	filters := indeedRemoteFilters(query)
	for i, filter := range filters {
		label := filter
		if label == "" {
			label = "onsite"
		}
		ReportProgress(ctx, Progress{
			Phase:         "indeed_filter",
			Keywords:      query["keywords"],
			Location:      query["location"],
			QueryIndex:    i + 1,
			QueryCount:    len(filters),
			JobsCollected: len(all),
			Message:       "indeed remote filter: " + label,
		})
		jobs, err := s.scrapeOnce(ctx, query, filter)
		all = append(all, jobs...)
		if err != nil {
			return all, err
		}
	}
	return all, nil
}

func indeedRemoteFilters(query map[string]string) []string {
	includeRemote := strings.EqualFold(strings.TrimSpace(query["include_remote"]), "true")
	includeHybrid := strings.EqualFold(strings.TrimSpace(query["include_hybrid"]), "true")
	switch {
	case includeRemote && includeHybrid:
		return []string{"remote", "hybrid"}
	case includeRemote:
		return []string{"remote"}
	case includeHybrid:
		return []string{"hybrid"}
	default:
		return []string{""}
	}
}

func indeedRadius(query map[string]string) string {
	radius := strings.TrimSpace(query["radius"])
	if db.ValidIndeedRadius(radius) {
		return radius
	}
	return "15"
}

func (s *IndeedScraper) scrapeOnce(ctx context.Context, query map[string]string, remoteFilter string) (jobs []JobData, err error) {
	var run apifyRun
	startFn := func() error {
		if s.budgetCents <= 0 {
			_, end := ApifyBudgetPeriod(s.client.now())
			return budgetBlockedError{periodEnd: end}
		}
		snap, err := s.client.AccountBudget(ctx, s.budgetCents)
		if err != nil {
			return err
		}
		if snap.RemainingCents <= 0 {
			return budgetBlockedError{periodEnd: snap.PeriodEnd}
		}
		input := indeedActorInput{
			Query:              query["keywords"],
			Country:            s.options.Country,
			Location:           query["location"],
			Radius:             indeedRadius(query),
			JobType:            s.options.JobType,
			FromDays:           s.options.FromDays,
			MaxRows:            s.options.MaxRows,
			EnableUniqueJobs:   s.options.EnableUniqueJobs,
			IncludeSimilarJobs: s.options.IncludeSimilarJobs,
		}
		if remoteFilter != "" {
			filter := remoteFilter
			input.Remote = &filter
		}
		run, err = s.client.startRun(ctx, ApifyIndeedActorID, snap.RemainingCents, input)
		return err
	}
	if s.AroundStart != nil {
		err = s.AroundStart(ctx, startFn)
	} else {
		err = startFn()
	}
	if err != nil {
		return nil, err
	}
	if run.ID != "" {
		defer func() {
			if ctx.Err() != nil {
				s.client.abort(run.ID)
			}
		}()
	}

	polled, err := s.client.pollRun(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	if isTerminalFailure(polled.Status) {
		_, waitErr := s.client.waitUsage(ctx, run.ID)
		if waitErr != nil {
			return nil, waitErr
		}
		return nil, fmt.Errorf("apify indeed run %s status %s", run.ID, polled.Status)
	}
	if !isTerminalSuccess(polled.Status) {
		return nil, fmt.Errorf("apify indeed run %s has unknown status %q", run.ID, polled.Status)
	}

	items, err := s.client.datasetItems(ctx, polled.DefaultDatasetID)
	if err != nil {
		msg := "apify dataset fetch failed"
		RecordAPIContract(ctx, APIContract{
			Phase:     "apify_dataset",
			DatasetID: polled.DefaultDatasetID,
			ErrorBody: truncateProgressString(err.Error(), progressErrorLimit),
			Message:   msg,
		})
		ReportProgress(ctx, Progress{
			Phase:       "apify_dataset",
			Source:      string(db.SourceIndeed),
			ApifyRunID:  polled.ID,
			ApifyStatus: polled.Status,
			Keywords:    query["keywords"],
			Location:    query["location"],
			Message:     msg,
		})
		return nil, err
	}
	scrapedAt := s.client.now()
	skipped := 0
	for _, item := range items {
		job, ok := MapIndeedItem(item, query["location"], scrapedAt)
		if !ok {
			skipped++
			continue
		}
		jobs = append(jobs, job)
	}
	msg := fmt.Sprintf("mapped %d of %d dataset items (%d skipped)", len(jobs), len(items), skipped)
	RecordAPIContract(ctx, datasetMapContract(polled.DefaultDatasetID, items, len(jobs), skipped, msg))
	ReportProgress(ctx, Progress{
		Phase:         "apify_dataset",
		Source:        string(db.SourceIndeed),
		ApifyRunID:    polled.ID,
		ApifyStatus:   polled.Status,
		Keywords:      query["keywords"],
		Location:      query["location"],
		JobsCollected: len(jobs),
		Message:       msg,
	})
	if _, waitErr := s.client.waitUsage(ctx, run.ID); waitErr != nil {
		return jobs, waitErr
	}
	return jobs, nil
}

// MapIndeedItem maps one actor dataset row onto JobData. ok is false when required fields are missing.
func MapIndeedItem(item map[string]any, searchLocation string, scrapedAt time.Time) (JobData, bool) {
	title := stringField(item, "title")
	company := stringField(item, "companyName", "company")
	rawURL := stringField(item, "jobUrl", "url")
	jobURL, ok := canonicalizeIndeedURL(rawURL)
	if !ok || title == "" || company == "" {
		return JobData{}, false
	}
	location := indeedLocationString(item)
	if location == "" {
		location = searchLocation
	}
	desc := stringField(item, "descriptionText")
	if desc == "" {
		desc = htmlToText(stringField(item, "descriptionHtml"))
	}
	date := parseIndeedPublished(stringField(item, "datePublished"), scrapedAt)
	remote := boolField(item, "isRemote") || containsRemote(location)
	return JobData{
		Title:       title,
		Company:     company,
		Location:    location,
		JobURL:      jobURL,
		Description: desc,
		Date:        date,
		Source:      db.SourceIndeed,
		IsRemote:    remote,
	}, true
}

func indeedLocationString(item map[string]any) string {
	if formatted := stringField(item, "formattedLocation"); formatted != "" {
		return formatted
	}
	switch loc := item["location"].(type) {
	case string:
		if s := strings.TrimSpace(loc); s != "" {
			return s
		}
	case map[string]any:
		if formatted := stringField(loc, "formattedLocationFull", "formattedLocation"); formatted != "" {
			return formatted
		}
		parts := make([]string, 0, 3)
		for _, key := range []string{"city", "admin1Code", "countryCode"} {
			if part := stringField(loc, key); part != "" {
				parts = append(parts, part)
			}
		}
		return strings.Join(parts, ", ")
	}
	return ""
}

func boolField(item map[string]any, key string) bool {
	switch v := item[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

func canonicalizeIndeedURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host != "indeed.com" && !strings.HasSuffix(host, ".indeed.com") {
		return "", false
	}
	// Keep jk query when present; drop tracking noise.
	q := u.Query()
	jk := strings.TrimSpace(q.Get("jk"))
	u.RawQuery = ""
	u.Fragment = ""
	u.RawFragment = ""
	if jk != "" {
		u.RawQuery = "jk=" + url.QueryEscape(jk)
	}
	return u.String(), true
}

func parseIndeedPublished(raw string, fallback time.Time) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts.UTC()
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts.UTC()
	}
	if ts, err := time.ParseInLocation("2006-01-02", raw, time.UTC); err == nil {
		return ts
	}
	return fallback
}
