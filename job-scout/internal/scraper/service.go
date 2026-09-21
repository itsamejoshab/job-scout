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
	Status         string          `json:"status"`
	JobSource      string          `json:"job_source"`
	ScrapedCount   int             `json:"scraped_count"`
	SavedCount     int             `json:"saved_count"`
	DuplicateCount int             `json:"duplicate_count"`
	SkippedCount   int             `json:"skipped_count,omitempty"`
	Error          string          `json:"error,omitempty"`
	Metadata       *ResultMetadata `json:"metadata,omitempty"`
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
		return NewIndeed(settings, s.apifyClient(), s.ApifyBudgetCents), nil
	case db.SourceDice:
		return NewDice(settings, s.apifyClient(), s.ApifyBudgetCents), nil
	default:
		return nil, fmt.Errorf("no scraper available for job source: %s", source)
	}
}

// RunTick scrapes enabled providers that are due (or forced).
func (s *Service) RunTick(ctx context.Context, in TickInput) (Result, error) {
	if isApifySource(db.JobSource(in.JobSource)) && strings.TrimSpace(s.ApifyToken) == "" {
		return Result{
			Status:    "skipped",
			JobSource: in.JobSource,
			Error:     ApifySetupMessage,
		}, nil
	}
	sources, err := s.ListDueProviders(ctx, in)
	if err != nil {
		return errResult(db.SourceLinkedIn, err), nil
	}
	if len(sources) == 0 {
		return Result{Status: "skipped", JobSource: in.JobSource}, nil
	}
	results := make([]Result, 0, len(sources))
	for _, source := range sources {
		res, err := s.ScrapeProvider(ctx, source)
		if err != nil {
			return res, err
		}
		results = append(results, res)
	}
	return CombineResults(results), nil
}

// ListDueProviders returns job sources that should scrape for this tick.
func (s *Service) ListDueProviders(ctx context.Context, in TickInput) ([]string, error) {
	if isApifySource(db.JobSource(in.JobSource)) && strings.TrimSpace(s.ApifyToken) == "" {
		return nil, nil
	}
	all, err := db.AllScraperSettings(ctx, s.db)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	cadences := make([]domain.ProviderCadence, 0, len(all))
	for _, st := range all {
		enabled := st.Enabled
		if isApifySource(st.JobSource) && strings.TrimSpace(s.ApifyToken) == "" {
			enabled = false
		}
		cadences = append(cadences, domain.ProviderCadence{
			Source:                string(st.JobSource),
			Enabled:               enabled,
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
	out := make([]string, 0, len(due))
	for _, p := range due {
		out = append(out, p.Source)
	}
	return out, nil
}

// ScrapeProvider scrapes one job source and persists new jobs.
func (s *Service) ScrapeProvider(ctx context.Context, source string) (Result, error) {
	ctx = WithContractRecorder(ctx)
	parsed, err := db.ParseJobSource(source)
	if err != nil {
		return attachResultMetadata(ctx, Result{Status: "error", JobSource: source, Error: err.Error()}), nil
	}
	if isApifySource(parsed) && strings.TrimSpace(s.ApifyToken) == "" {
		return attachResultMetadata(ctx, Result{
			Status:    "skipped",
			JobSource: string(parsed),
			Error:     ApifySetupMessage,
		}), nil
	}
	res, err := s.scrapeSource(ctx, parsed)
	return attachResultMetadata(ctx, res), err
}

// RunFullScrape is the debug sync path: same enabled/lock/insert rules as a
// forced tick for one source. It may ignore cadence.
func (s *Service) RunFullScrape(ctx context.Context, source db.JobSource) (Result, error) {
	ctx = WithContractRecorder(ctx)
	if isApifySource(source) && strings.TrimSpace(s.ApifyToken) == "" {
		return attachResultMetadata(ctx, Result{
			Status:    "skipped",
			JobSource: string(source),
			Error:     ApifySetupMessage,
		}), nil
	}
	settings, err := db.GetScraperSettings(ctx, s.db, source)
	if err != nil {
		return attachResultMetadata(ctx, errResult(source, err)), nil
	}
	if settings == nil {
		return attachResultMetadata(ctx, errResult(source, fmt.Errorf("no scraper settings found in database for job source: %s", source))), nil
	}
	if !settings.Enabled {
		return attachResultMetadata(ctx, Result{Status: "skipped", JobSource: string(source), Error: "provider disabled"}), nil
	}
	res, err := s.scrapeSource(ctx, source)
	return attachResultMetadata(ctx, res), err
}

func (s *Service) scrapeSource(ctx context.Context, source db.JobSource) (Result, error) {
	slog.Info("starting full scrape", "source", source)

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
	if isApifySource(source) && strings.TrimSpace(s.ApifyToken) == "" {
		return Result{Status: "skipped", JobSource: string(source), Error: ErrApifyTokenMissing.Error()}, nil
	}
	attachApifyStartLock(provider, conn, s.sleep)

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

	ReportProgress(ctx, Progress{
		Phase:      "starting",
		Source:     string(source),
		Rounds:     rounds,
		QueryCount: len(queries),
		Message:    "scrape started",
	})

queryLoop:
	for round := 0; round < rounds; round++ {
		if !isApifySource(source) {
			for _, keywords := range scraperSettings.GlobalSearches {
				ReportProgress(ctx, Progress{
					Phase:         "global",
					Source:        string(source),
					Round:         round + 1,
					Rounds:        rounds,
					Keywords:      keywords,
					JobsCollected: len(all),
					Message:       "global search",
				})
				query := map[string]string{"keywords": keywords}
				jobs, err := provider.ScrapeJobs(ctx, query)
				searchContext := globalSearchContext(keywords)
				stampSearchContext(jobs, searchContext)
				stampSearchIntention(jobs, db.SearchIntentionOnsite)
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
			if isApifySource(source) {
				if rem, ok := activityRemaining(ctx); ok && rem < DiceMinRemainingStart {
					leftRounds := rounds - round
					leftThisRound := len(queries) - i
					skipped += leftThisRound + (leftRounds-1)*len(queries)
					slog.Info("apify scrape skipped remaining pairs; activity time low", "source", source, "skipped", skipped)
					ReportProgress(ctx, Progress{
						Phase:         "time_budget",
						Source:        string(source),
						Round:         round + 1,
						Rounds:        rounds,
						QueryIndex:    i + 1,
						QueryCount:    len(queries),
						JobsCollected: len(all),
						Message:       "skipped remaining pairs; activity time low",
					})
					break queryLoop
				}
			}
			ReportProgress(ctx, Progress{
				Phase:         "query",
				Source:        string(source),
				Round:         round + 1,
				Rounds:        rounds,
				QueryIndex:    i + 1,
				QueryCount:    len(queries),
				Keywords:      query["keywords"],
				Location:      query["location"],
				JobsCollected: len(all),
				Message:       fmt.Sprintf("query %d/%d round %d/%d", i+1, len(queries), round+1, rounds),
			})
			jobs, err := provider.ScrapeJobs(ctx, query)
			searchContext := querySearchContext(source, query)
			stampSearchContext(jobs, searchContext)
			if source != db.SourceIndeed {
				stampSearchIntention(jobs, DeriveSearchIntention(source, query, ""))
			}
			slog.Debug("search completed", "source", source, "round", round+1, "search_context", searchContext, "matches", len(jobs))
			all = append(all, jobs...)
			if err != nil {
				slog.Error("scraping query failed", "round", round+1, "query", query, "err", err)
				scrapeErr = err
				if isApifySource(source) {
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

func isApifySource(source db.JobSource) bool {
	return source == db.SourceDice || source == db.SourceIndeed
}

func attachApifyStartLock(provider Provider, conn *sql.Conn, sleepFn func(context.Context, time.Duration) error) {
	if sleepFn == nil {
		sleepFn = sleep
	}
	around := func(ctx context.Context, fn func() error) error {
		for {
			locked, err := db.TryLockKey(ctx, conn, ApifyLockKey)
			if err != nil {
				return err
			}
			if locked {
				defer func() {
					unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := db.UnlockKey(unlockCtx, conn, ApifyLockKey); err != nil {
						slog.Error("apify unlock failed", "err", err)
					}
				}()
				return fn()
			}
			slog.Info("apify start waiting for advisory lock")
			ReportProgress(ctx, Progress{
				Phase:   "apify_lock_wait",
				Message: "waiting for apify start lock",
			})
			if err := sleepFn(ctx, ApifyLockRetryDelay); err != nil {
				return ErrApifyLockBusy
			}
		}
	}
	switch p := provider.(type) {
	case *DiceScraper:
		p.AroundStart = around
	case *IndeedScraper:
		p.AroundStart = around
	}
}

func (s *Service) sleep(ctx context.Context, d time.Duration) error {
	if s.Sleep != nil {
		return s.Sleep(ctx, d)
	}
	return sleep(ctx, d)
}

func activityRemaining(ctx context.Context) (time.Duration, bool) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0, false
	}
	return time.Until(deadline), true
}

func (s *Service) finishScrape(ctx context.Context, source db.JobSource, all []JobData, scrapeErr error, skipped int) (Result, error) {
	ReportProgress(ctx, Progress{
		Phase:         "saving",
		Source:        string(source),
		JobsCollected: len(all),
		Message:       "persisting scraped jobs",
	})
	saved, err := s.saveJobs(ctx, all)
	if err != nil {
		return errResult(source, err), nil
	}

	now := s.now()
	if scrapeErr != nil {
		if errors.Is(scrapeErr, ErrApifyLockBusy) {
			slog.Info("apify scrape skipped, start lock busy", "source", source)
			return Result{
				Status:         "skipped",
				JobSource:      string(source),
				ScrapedCount:   len(all),
				SavedCount:     saved,
				DuplicateCount: len(all) - saved,
				SkippedCount:   skipped,
				Error:          scrapeErr.Error(),
			}, nil
		}
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
			JobSource:       j.Source,
			Title:           j.Title,
			Company:         j.Company,
			Description:     desc,
			Location:        j.Location,
			Date:            j.Date,
			JobURL:          j.JobURL,
			IsRemote:        j.IsRemote,
			SearchContext:   j.SearchContext,
			SearchIntention: j.SearchIntention,
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

func CombineResults(results []Result) Result {
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
		out.Metadata = results[0].Metadata
	} else {
		var calls []APIContract
		for _, r := range results {
			if r.Metadata == nil {
				continue
			}
			for _, call := range r.Metadata.APICalls {
				if len(calls) >= maxAPIContracts {
					break
				}
				calls = append(calls, call)
			}
			if len(calls) >= maxAPIContracts {
				break
			}
		}
		if len(calls) > 0 {
			out.Metadata = &ResultMetadata{APICalls: calls}
		}
	}
	out.Error = strings.Join(errs, "; ")
	return out
}

func stampSearchContext(jobs []JobData, searchContext string) {
	for i := range jobs {
		jobs[i].SearchContext = searchContext
	}
}

func stampSearchIntention(jobs []JobData, intention string) {
	for i := range jobs {
		jobs[i].SearchIntention = intention
	}
}

func querySearchContext(source db.JobSource, query map[string]string) string {
	switch source {
	case db.SourceDice:
		return fmt.Sprintf(
			`query keywords=%q location=%q include_remote=%q`,
			query["keywords"],
			query["location"],
			query["include_remote"],
		)
	case db.SourceIndeed:
		return fmt.Sprintf(
			`query keywords=%q location=%q include_remote=%q include_hybrid=%q`,
			query["keywords"],
			query["location"],
			query["include_remote"],
			query["include_hybrid"],
		)
	default:
		return fmt.Sprintf(
			`query keywords=%q location=%q f_WT=%q`,
			query["keywords"],
			query["location"],
			query["f_WT"],
		)
	}
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
