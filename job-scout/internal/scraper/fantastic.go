package scraper

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jobscout/jobscout/internal/db"
)

type fantasticActorInput struct {
	AIEmploymentTypeFilter          []string `json:"aiEmploymentTypeFilter"`
	AIHasSalary                     bool     `json:"aiHasSalary"`
	AIVisaSponsorshipFilter         bool     `json:"aiVisaSponsorshipFilter"`
	AIWorkArrangementFilter         []string `json:"aiWorkArrangementFilter"`
	DescriptionType                 string   `json:"descriptionType"`
	HasNoLocation                   bool     `json:"hasNoLocation"`
	HasSalary                       bool     `json:"hasSalary"`
	IncludeCompanyDetails           bool     `json:"includeCompanyDetails"`
	IncludeLinkedIn                 bool     `json:"includeLinkedIn"`
	Limit                           int      `json:"limit"`
	LocationExclusionSearch         []string `json:"locationExclusionSearch"`
	LocationSearch                  []string `json:"locationSearch"`
	PopulateAiRemoteLocation        bool     `json:"populateAiRemoteLocation"`
	PopulateAiRemoteLocationDerived bool     `json:"populateAiRemoteLocationDerived"`
	RemoteOnlyLegacy                bool     `json:"remote only (legacy)"`
	RemoveAgency                    bool     `json:"removeAgency"`
	TitleExclusionSearch            []string `json:"titleExclusionSearch"`
	TitleSearch                     []string `json:"titleSearch"`
}

// FantasticScraper starts one Apify Career Site Job Listing Feed run per query.
type FantasticScraper struct {
	settings    db.ScraperSettings
	queries     []db.FantasticQuery
	client      *ApifyClient
	budgetCents int
	AroundStart func(ctx context.Context, fn func() error) error
}

func NewFantastic(settings db.ScraperSettings, client *ApifyClient, budgetCents int) *FantasticScraper {
	opts, err := db.ParseFantasticOptions(settings.ProviderOptions)
	if err != nil || len(opts.Queries) == 0 {
		opts = db.DefaultFantasticOptions()
	}
	return &FantasticScraper{
		settings:    settings,
		queries:     opts.Queries,
		client:      client,
		budgetCents: budgetCents,
	}
}

func (s *FantasticScraper) Source() db.JobSource { return db.SourceFantastic }

func (s *FantasticScraper) ScrapeJobs(ctx context.Context, query map[string]string) (jobs []JobData, err error) {
	if s.client == nil {
		return nil, fmt.Errorf("apify client is required")
	}
	var run apifyRun
	input := fantasticActorInputFromQuery(s.queryFor(query))
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
		run, err = s.client.startRun(ctx, ApifyFantasticActorID, snap.RemainingCents, input)
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
		return nil, fmt.Errorf("apify fantastic run %s status %s", run.ID, polled.Status)
	}
	if !isTerminalSuccess(polled.Status) {
		return nil, fmt.Errorf("apify fantastic run %s has unknown status %q", run.ID, polled.Status)
	}

	items, err := s.client.datasetItems(ctx, polled.DefaultDatasetID)
	if err != nil {
		RecordAPIContract(ctx, APIContract{
			Phase:     "apify_dataset",
			DatasetID: polled.DefaultDatasetID,
			ErrorBody: truncateProgressString(err.Error(), progressErrorLimit),
			Message:   "apify dataset fetch failed",
		})
		return nil, err
	}
	scrapedAt := s.client.now()
	skipped := 0
	searchLocation := strings.Join(input.LocationSearch, ", ")
	if fallback := strings.TrimSpace(query["location"]); searchLocation == "" {
		searchLocation = fallback
	}
	for _, item := range items {
		job, ok := MapFantasticItem(item, searchLocation, scrapedAt)
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
		Source:        string(db.SourceFantastic),
		ApifyRunID:    polled.ID,
		ApifyStatus:   polled.Status,
		Keywords:      strings.Join(input.TitleSearch, ", "),
		Location:      searchLocation,
		JobsCollected: len(jobs),
		Message:       msg,
	})
	if _, waitErr := s.client.waitUsage(ctx, run.ID); waitErr != nil {
		return jobs, waitErr
	}
	return jobs, nil
}

func (s *FantasticScraper) queryFor(query map[string]string) db.FantasticQuery {
	if len(s.queries) == 0 {
		return db.DefaultFantasticQuery()
	}
	idx, err := strconv.Atoi(strings.TrimSpace(query["query_index"]))
	if err != nil || idx < 0 || idx >= len(s.queries) {
		return s.queries[0]
	}
	return s.queries[idx]
}

func fantasticActorInputFromQuery(query db.FantasticQuery) fantasticActorInput {
	limit := query.Limit
	if limit < db.FantasticMinLimit {
		limit = db.FantasticMinLimit
	}
	if limit > db.FantasticMaxLimit {
		limit = db.FantasticMaxLimit
	}
	return fantasticActorInput{
		AIEmploymentTypeFilter:          query.AIEmploymentTypeFilter,
		AIHasSalary:                     false,
		AIVisaSponsorshipFilter:         false,
		AIWorkArrangementFilter:         query.AIWorkArrangementFilter,
		DescriptionType:                 "text",
		HasNoLocation:                   false,
		HasSalary:                       false,
		IncludeCompanyDetails:           false,
		IncludeLinkedIn:                 false,
		Limit:                           limit,
		LocationExclusionSearch:         query.LocationExclusionSearch,
		LocationSearch:                  query.LocationSearch,
		PopulateAiRemoteLocation:        false,
		PopulateAiRemoteLocationDerived: false,
		RemoteOnlyLegacy:                false,
		RemoveAgency:                    true,
		TitleExclusionSearch:            query.TitleExclusionSearch,
		TitleSearch:                     query.TitleSearch,
	}
}

func fantasticQueryMaps(settings db.ScraperSettings) []map[string]string {
	opts, err := db.ParseFantasticOptions(settings.ProviderOptions)
	if err != nil || len(opts.Queries) == 0 {
		opts = db.DefaultFantasticOptions()
	}
	out := make([]map[string]string, 0, len(opts.Queries))
	for i, query := range opts.Queries {
		out = append(out, map[string]string{
			"keywords":                strings.Join(query.TitleSearch, ", "),
			"location":                strings.Join(query.LocationSearch, ", "),
			"query_index":             strconv.Itoa(i),
			"aiWorkArrangementFilter": strings.Join(query.AIWorkArrangementFilter, ","),
			"aiEmploymentTypeFilter":  strings.Join(query.AIEmploymentTypeFilter, ","),
			"titleExclusionSearch":    strings.Join(query.TitleExclusionSearch, ", "),
			"locationExclusionSearch": strings.Join(query.LocationExclusionSearch, ", "),
			"limit":                   strconv.Itoa(query.Limit),
		})
	}
	return out
}

// MapFantasticItem maps one actor dataset row onto JobData. ok is false when required fields are missing.
func MapFantasticItem(item map[string]any, searchLocation string, scrapedAt time.Time) (JobData, bool) {
	title := stringField(item, "title")
	company := stringField(item, "organization", "company")
	rawURL := stringField(item, "url")
	jobURL, ok := canonicalizeHTTPURL(rawURL)
	if !ok || title == "" || company == "" {
		return JobData{}, false
	}
	location := fantasticLocationString(item)
	if location == "" {
		location = searchLocation
	}
	desc := stringField(item, "description_text")
	if desc == "" {
		desc = htmlToText(stringField(item, "description_html"))
	}
	date := parseDicePosted(stringField(item, "date_posted"), scrapedAt)
	arrangement := stringField(item, "ai_work_arrangement")
	remote := containsRemote(arrangement) ||
		strings.EqualFold(stringField(item, "location_type"), "TELECOMMUTE") ||
		containsRemote(location)
	return JobData{
		Title:       title,
		Company:     company,
		Location:    location,
		JobURL:      jobURL,
		Description: desc,
		Date:        date,
		Source:      db.SourceFantastic,
		IsRemote:    remote,
	}, true
}

func fantasticLocationString(item map[string]any) string {
	if parts := anyStringList(item["locations_derived"]); len(parts) > 0 {
		return strings.Join(parts, "; ")
	}
	if alt := stringField(item, "locations_alt"); alt != "" {
		return alt
	}
	return ""
}

func canonicalizeHTTPURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	if strings.TrimSpace(u.Host) == "" {
		return "", false
	}
	u.Fragment = ""
	u.RawFragment = ""
	return u.String(), true
}

func anyStringList(v any) []string {
	switch x := v.(type) {
	case []string:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			switch part := item.(type) {
			case string:
				if s := strings.TrimSpace(part); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	case string:
		if s := strings.TrimSpace(x); s != "" {
			return []string{s}
		}
	}
	return nil
}
