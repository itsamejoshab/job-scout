package scraper

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/domain"
)

// sleepBetween throttles requests between queries/URLs, mirroring the old 1s delay.
var sleepBetween = 1 * time.Second

// Result is the outcome of a full scrape, returned by the scrape activity and
// surfaced by the workflow / API. JSON-serializable for Temporal payloads.
type Result struct {
	Status         string `json:"status"`
	JobSource      string `json:"job_source"`
	ScrapedCount   int    `json:"scraped_count"`
	SavedCount     int    `json:"saved_count"`
	DuplicateCount int    `json:"duplicate_count"`
	SkippedCount   int    `json:"skipped_count,omitempty"`
	Error          string `json:"error,omitempty"`
}

// TickInput is the scrape request from ScrapeWorkflow / POST /run.
type TickInput struct {
	Force     bool   `json:"force"`
	JobSource string `json:"job_source,omitempty"`
}

// Service orchestrates providers and persistence, mirroring the Python
// ScraperService.
type Service struct {
	db               *sql.DB
	HTTPClient       *http.Client
	HTTPTimeout      time.Duration
	ErrorBackoff     time.Duration
	ApifyToken       string
	ApifyBudgetCents int
	ApifyBaseURL     string
	Now              func() time.Time
	Sleep            func(context.Context, time.Duration) error
}

func NewService(database *sql.DB) *Service {
	return &Service{db: database}
}

func NewServiceWithConfig(database *sql.DB, cfg config.Config) *Service {
	s := NewService(database)
	s.HTTPTimeout = time.Duration(cfg.HTTPTimeoutSeconds) * time.Second
	s.ErrorBackoff = time.Duration(cfg.ScrapeErrorBackoffSeconds) * time.Second
	s.ApifyToken = cfg.ApifyAPIToken
	s.ApifyBudgetCents = cfg.ApifyMonthlyBudgetCents
	return s
}

func (s *Service) timeout() time.Duration {
	if s.HTTPTimeout > 0 {
		return s.HTTPTimeout
	}
	return 30 * time.Second
}

func (s *Service) backoff() time.Duration {
	if s.ErrorBackoff > 0 {
		return s.ErrorBackoff
	}
	return 300 * time.Second
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) apifyClient() *ApifyClient {
	client := &ApifyClient{
		BaseURL: s.ApifyBaseURL,
		Token:   s.ApifyToken,
		Now:     s.Now,
		Sleep:   s.Sleep,
	}
	if s.HTTPClient != nil {
		client.HTTP = s.HTTPClient
	} else {
		client.HTTP = &http.Client{Timeout: s.timeout()}
	}
	return client
}

func (s *Service) newProvider(source db.JobSource, settings db.ScraperSettings) (Provider, error) {
	switch source {
	case db.SourceLinkedIn:
		li := NewLinkedInWithTimeout(settings, s.timeout())
		if s.HTTPClient != nil {
			li.client = s.HTTPClient
		}
		return li, nil
	case db.SourceIndeed:
		return NewIndeed(settings), nil
	case db.SourceDice:
		return NewDice(settings, s.apifyClient(), s.ApifyBudgetCents), nil
	default:
		return nil, fmt.Errorf("no scraper available for job source: %s", source)
	}
}

// RunTick scrapes enabled providers that are due (or forced).
func (s *Service) RunTick(ctx context.Context, in TickInput) (Result, error) {
	all, err := db.AllScraperSettings(ctx, s.db)
	if err != nil {
		return errResult(db.SourceLinkedIn, err), nil
	}
	now := time.Now()
	cadences := make([]domain.ProviderCadence, 0, len(all))
	for _, st := range all {
		cadences = append(cadences, domain.ProviderCadence{
			Source:                string(st.JobSource),
			Enabled:               st.Enabled,
			ScrapeIntervalSeconds: st.ScrapeIntervalSeconds,
			LastScrapedAt:         st.LastScrapedAt,
			NextEligibleAt:        st.NextEligibleAt,
		})
	}
	due := domain.DueProviders(cadences, now, in.Force)
	if in.JobSource != "" {
		filtered := make([]domain.ProviderCadence, 0, 1)
		for _, p := range due {
			if p.Source == in.JobSource {
				filtered = append(filtered, p)
			}
		}
		due = filtered
	}
	if len(due) == 0 {
		return Result{Status: "skipped", JobSource: in.JobSource}, nil
	}

	var results []Result
	for _, p := range due {
		res, err := s.scrapeSource(ctx, db.JobSource(p.Source))
		if err != nil {
			return res, err
		}
		results = append(results, res)
	}
	return combineTickResults(results), nil
}

// RunFullScrape is the debug sync path: same enabled/lock/insert rules as a
// forced tick for one source. It may ignore cadence.
func (s *Service) RunFullScrape(ctx context.Context, source db.JobSource) (Result, error) {
	if source == db.SourceIndeed {
		return Result{
			Status:    "skipped",
			JobSource: string(source),
			Error:     "indeed scraper is not implemented",
		}, nil
	}
	settings, err := db.GetScraperSettings(ctx, s.db, source)
	if err != nil {
		return errResult(source, err), nil
	}
	if settings == nil {
		return errResult(source, fmt.Errorf("no scraper settings found in database for job source: %s", source)), nil
	}
	if !settings.Enabled {
		return Result{Status: "skipped", JobSource: string(source), Error: "provider disabled"}, nil
	}
	return s.scrapeSource(ctx, source)
}

func (s *Service) scrapeSource(ctx context.Context, source db.JobSource) (Result, error) {
	slog.Info("starting full scrape", "source", source)

	if source == db.SourceIndeed {
		return Result{
			Status:    "skipped",
			JobSource: string(source),
			Error:     "indeed scraper is not implemented",
		}, nil
	}

	universal, err := db.GetSearchSettings(ctx, s.db)
	if err != nil {
		return errResult(source, err), nil
	}
	if universal == nil {
		return errResult(source, fmt.Errorf("no universal search settings found in database")), nil
	}

	scraperSettings, err := db.GetScraperSettings(ctx, s.db, source)
	if err != nil {
		return errResult(source, err), nil
	}
	if scraperSettings == nil {
		return errResult(source, fmt.Errorf("no scraper settings found in database for job source: %s", source)), nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errResult(source, err), nil
	}
	defer conn.Close()

	locked, err := db.TryLockJobSource(ctx, conn, source)
	if err != nil {
		return errResult(source, err), nil
	}
	if !locked {
		slog.Info("scrape skipped, advisory lock busy", "source", source)
		return Result{Status: "skipped", JobSource: string(source), Error: "advisory lock busy"}, nil
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := db.UnlockJobSource(unlockCtx, conn, source); err != nil {
			slog.Error("advisory unlock failed; discarding connection", "source", source, "err", err)
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()

	provider, err := s.newProvider(source, *scraperSettings)
	if err != nil {
		return errResult(source, err), nil
	}
	if source == db.SourceDice && strings.TrimSpace(s.ApifyToken) == "" {
		return Result{Status: "skipped", JobSource: string(source), Error: ErrApifyTokenMissing.Error()}, nil
	}
	if dice, ok := provider.(*DiceScraper); ok {
		dice.AroundStart = func(ctx context.Context, fn func() error) error {
			locked, err := db.TryLockKey(ctx, conn, ApifyLockKey)
			if err != nil {
				return err
			}
			if !locked {
				return ErrApifyLockBusy
			}
			defer func() {
				unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := db.UnlockKey(unlockCtx, conn, ApifyLockKey); err != nil {
					slog.Error("apify unlock failed", "err", err)
				}
			}()
			return fn()
		}
	}

	var (
		all       []JobData
		scrapeErr error
		skipped   int
	)

	rounds := scraperSettings.Rounds
	if rounds < 1 {
		rounds = 1
	}
	if rounds > 3 {
		rounds = 3
	}
	queries := scraperSettings.SearchQueries
	if source == db.SourceLinkedIn {
		queries = combineLinkedInSearchQueries(queries)
	}

queryLoop:
	for round := 0; round < rounds; round++ {
		if source != db.SourceDice {
			for _, keywords := range scraperSettings.GlobalSearches {
				query := map[string]string{"keywords": keywords}
				jobs, err := provider.ScrapeJobs(ctx, query)
				searchContext := globalSearchContext(keywords)
				stampSearchContext(jobs, searchContext)
				slog.Debug("search completed", "source", source, "round", round+1, "search_context", searchContext, "matches", len(jobs))
				all = append(all, jobs...)
				if err != nil {
					slog.Error("scraping global search failed", "round", round+1, "keywords", keywords, "err", err)
					scrapeErr = err
				}
				if err := sleep(ctx, sleepBetween); err != nil {
					return s.finishScrape(ctx, source, all, err, skipped)
				}
			}
		}

		for i, query := range queries {
			if source == db.SourceDice {
				if rem, ok := activityRemaining(ctx); ok && rem < DiceMinRemainingStart {
					leftRounds := rounds - round
					leftThisRound := len(queries) - i
					skipped += leftThisRound + (leftRounds-1)*len(queries)
					slog.Info("dice scrape skipped remaining pairs; activity time low", "skipped", skipped)
					break queryLoop
				}
			}
			jobs, err := provider.ScrapeJobs(ctx, query)
			searchContext := querySearchContext(source, query)
			stampSearchContext(jobs, searchContext)
			slog.Debug("search completed", "source", source, "round", round+1, "search_context", searchContext, "matches", len(jobs))
			all = append(all, jobs...)
			if err != nil {
				slog.Error("scraping query failed", "round", round+1, "query", query, "err", err)
				scrapeErr = err
				if source == db.SourceDice {
					break queryLoop
				}
			}
			if err := sleep(ctx, sleepBetween); err != nil {
				return s.finishScrape(ctx, source, all, err, skipped)
			}
		}
	}

	return s.finishScrape(ctx, source, all, scrapeErr, skipped)
}

func activityRemaining(ctx context.Context) (time.Duration, bool) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0, false
	}
	return time.Until(deadline), true
}

func (s *Service) finishScrape(ctx context.Context, source db.JobSource, all []JobData, scrapeErr error, skipped int) (Result, error) {
	saved, err := s.saveJobs(ctx, all)
	if err != nil {
		return errResult(source, err), nil
	}

	now := s.now()
	if scrapeErr != nil {
		if !IsApifyTokenMissing(scrapeErr) {
			next := now.Add(s.backoff())
			if IsApifyBudgetBlocked(scrapeErr) {
				var blocked budgetBlockedError
				if errors.As(scrapeErr, &blocked) && !blocked.periodEnd.IsZero() {
					next = blocked.periodEnd
				} else {
					_, next = ApifyBudgetPeriod(now)
				}
			}
			if markErr := db.MarkScrapeFailure(ctx, s.db, source, next); markErr != nil {
				slog.Error("mark scrape failure failed", "err", markErr)
			}
		}
		res := errResult(source, scrapeErr)
		res.ScrapedCount = len(all)
		res.SavedCount = saved
		res.DuplicateCount = len(all) - saved
		res.SkippedCount = skipped
		return res, nil
	}

	if err := db.MarkScrapeSuccess(ctx, s.db, source, now); err != nil {
		return errResult(source, err), nil
	}

	slog.Info("full scrape complete", "source", source, "scraped", len(all), "saved", saved, "skipped", skipped)
	return Result{
		Status:         "success",
		JobSource:      string(source),
		ScrapedCount:   len(all),
		SavedCount:     saved,
		DuplicateCount: len(all) - saved,
		SkippedCount:   skipped,
	}, nil
}

func (s *Service) saveJobs(ctx context.Context, jobs []JobData) (int, error) {
	saved := 0
	for _, j := range jobs {
		var desc *string
		if j.Description != "" {
			d := j.Description
			desc = &d
		}
		inserted, err := db.InsertJobIfNew(ctx, s.db, db.Job{
			JobSource:     j.Source,
			Title:         j.Title,
			Company:       j.Company,
			Description:   desc,
			Location:      j.Location,
			Date:          j.Date,
			JobURL:        j.JobURL,
			IsRemote:      j.IsRemote,
			SearchContext: j.SearchContext,
		})
		if err != nil {
			slog.Error("saving job failed", "title", j.Title, "err", err)
			continue
		}
		if inserted {
			saved++
		}
	}
	return saved, nil
}

func combineTickResults(results []Result) Result {
	if len(results) == 0 {
		return Result{Status: "skipped"}
	}
	var out Result
	var errs []string
	var lastStatus string
	anyErr := false
	for _, r := range results {
		out.ScrapedCount += r.ScrapedCount
		out.SavedCount += r.SavedCount
		out.DuplicateCount += r.DuplicateCount
		out.SkippedCount += r.SkippedCount
		if r.Error != "" {
			errs = append(errs, r.Error)
		}
		if r.Status == "error" {
			anyErr = true
		}
		if r.Status != "" {
			lastStatus = r.Status
		}
	}
	if anyErr {
		out.Status = "error"
	} else {
		out.Status = lastStatus
	}
	if len(results) == 1 {
		out.JobSource = results[0].JobSource
	}
	out.Error = strings.Join(errs, "; ")
	return out
}

func stampSearchContext(jobs []JobData, searchContext string) {
	for i := range jobs {
		jobs[i].SearchContext = searchContext
	}
}

func querySearchContext(source db.JobSource, query map[string]string) string {
	if source == db.SourceDice {
		return fmt.Sprintf(
			`query keywords=%q location=%q include_remote=%q`,
			query["keywords"],
			query["location"],
			query["include_remote"],
		)
	}
	return fmt.Sprintf(
		`query keywords=%q location=%q f_WT=%q`,
		query["keywords"],
		query["location"],
		query["f_WT"],
	)
}

func globalSearchContext(keywords string) string {
	return fmt.Sprintf(`global keywords=%q`, keywords)
}

// FetchJobDescription GETs LinkedIn job-detail HTML using the configured timeout.
func (s *Service) FetchJobDescription(ctx context.Context, jobURL string) (string, error) {
	li := NewLinkedInWithTimeout(db.ScraperSettings{}, s.timeout())
	if s.HTTPClient != nil {
		timeout := s.timeout()
		client := *s.HTTPClient
		if client.Timeout <= 0 {
			client.Timeout = timeout
		}
		li.client = &client
	}
	return li.FetchJobDescription(ctx, jobURL)
}

func errResult(source db.JobSource, err error) Result {
	return Result{
		Status:    "error",
		JobSource: string(source),
		Error:     err.Error(),
	}
}
