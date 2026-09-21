package scraper

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jobscout/jobscout/internal/db"
	"golang.org/x/net/html"
)

const linkedInUserAgent = "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/109.0.0.0 Safari/537.36"

const linkedInBaseURL = "https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search"

var linkedInPagePause = 5 * time.Second

// LinkedIn guest search 429s after enough paging; retry the same GET in-place
// instead of failing the whole scrape. Temporal does not retry the activity.
var (
	linkedInRetryAttempts  = 5
	linkedInRetryInitial   = 4 * time.Second
	linkedInRetryMaxWait   = 60 * time.Second
	linkedInRetryHeartbeat = 30 * time.Second
)

// linkedInWorkTypeOrder is the natural-language order for work-type phrases.
// LinkedIn ignores f_WT URL filters; selected types are prepended to keywords.
var linkedInWorkTypeOrder = []struct {
	code  string
	label string
}{
	{"2", "Remote"},
	{"3", "Hybrid"},
	{"1", "On-Site"},
}

// LinkedInScraper ports the original LinkedIn guest-API scraper to net/http +
// golang.org/x/net/html.
type LinkedInScraper struct {
	timespanCode  string
	pagesToScrape int
	client        *http.Client
	sleep         func(context.Context, time.Duration) error
}

func NewLinkedIn(settings db.ScraperSettings) *LinkedInScraper {
	return NewLinkedInWithTimeout(settings, 30*time.Second)
}

func NewLinkedInWithTimeout(settings db.ScraperSettings, timeout time.Duration) *LinkedInScraper {
	ts := settings.TimespanCode
	if ts == "" {
		ts = "r84600"
	}
	pages := settings.PagesToScrape
	if pages < 1 {
		pages = 1
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &LinkedInScraper{
		timespanCode:  ts,
		pagesToScrape: pages,
		client:        &http.Client{Timeout: timeout},
	}
}

func (s *LinkedInScraper) Source() db.JobSource { return db.SourceLinkedIn }

// parseLinkedInWorkTypes returns selected work-type codes in phrase order.
// Empty or all three codes mean unrestricted (no keyword prefix).
func parseLinkedInWorkTypes(fWT string) []string {
	fWT = strings.TrimSpace(fWT)
	if fWT == "" {
		return nil
	}
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(fWT, func(r rune) bool {
		return r == ',' || r == '|' || r == ' '
	}) {
		switch part {
		case "1", "2", "3":
			seen[part] = true
		}
	}
	if len(seen) == 0 || (seen["1"] && seen["2"] && seen["3"]) {
		return nil
	}
	out := make([]string, 0, len(seen))
	for _, wt := range linkedInWorkTypeOrder {
		if seen[wt.code] {
			out = append(out, wt.code)
		}
	}
	return out
}

func linkedInWorkTypePhrase(codes []string) string {
	if len(codes) == 0 {
		return ""
	}
	labels := make([]string, 0, len(codes))
	for _, code := range codes {
		for _, wt := range linkedInWorkTypeOrder {
			if wt.code == code {
				labels = append(labels, wt.label)
				break
			}
		}
	}
	switch len(labels) {
	case 0:
		return ""
	case 1:
		return labels[0]
	case 2:
		return labels[0] + " or " + labels[1]
	default:
		return strings.Join(labels[:len(labels)-1], ", ") + " or " + labels[len(labels)-1]
	}
}

func effectiveLinkedInKeywords(keywords, fWT string) string {
	phrase := linkedInWorkTypePhrase(parseLinkedInWorkTypes(fWT))
	if phrase == "" {
		return keywords
	}
	return phrase + " " + keywords
}

// combineLinkedInSearchQueries merges legacy one-code-per-row settings into one
// query per keywords+location with a comma-separated f_WT list.
func combineLinkedInSearchQueries(queries []map[string]string) []map[string]string {
	type key struct{ keywords, location string }
	order := make([]key, 0, len(queries))
	codesByKey := make(map[key]map[string]bool, len(queries))
	unrestricted := make(map[key]bool, len(queries))

	for _, q := range queries {
		k := key{keywords: q["keywords"], location: q["location"]}
		if _, ok := codesByKey[k]; !ok {
			order = append(order, k)
			codesByKey[k] = map[string]bool{}
		}
		codes := parseLinkedInWorkTypes(q["f_WT"])
		if codes == nil {
			unrestricted[k] = true
			continue
		}
		for _, code := range codes {
			codesByKey[k][code] = true
		}
	}

	out := make([]map[string]string, 0, len(order))
	for _, k := range order {
		fWT := ""
		if !unrestricted[k] {
			parts := make([]string, 0, 3)
			for _, wt := range linkedInWorkTypeOrder {
				if codesByKey[k][wt.code] {
					parts = append(parts, wt.code)
				}
			}
			if len(parts) > 0 && len(parts) < 3 {
				fWT = strings.Join(parts, ",")
			}
		}
		out = append(out, map[string]string{
			"keywords": k.keywords,
			"location": k.location,
			"f_WT":     fWT,
		})
	}
	return out
}

func (s *LinkedInScraper) buildSearchURL(query map[string]string, start int) string {
	params := []string{}
	if kw := query["keywords"]; kw != "" {
		params = append(params, "keywords="+url.QueryEscape(effectiveLinkedInKeywords(kw, query["f_WT"])))
	}
	if loc := query["location"]; loc != "" {
		params = append(params, "geoId="+loc)
	}
	// f_WT is settings-only; LinkedIn guest search ignores it as a URL filter.
	params = append(params, "f_TPR="+s.timespanCode, fmt.Sprintf("start=%d", start))
	return linkedInBaseURL + "?" + strings.Join(params, "&")
}

func (s *LinkedInScraper) ScrapeJobs(ctx context.Context, query map[string]string) ([]JobData, error) {
	if query["keywords"] == "" {
		return nil, fmt.Errorf("invalid search query: missing keywords")
	}

	var jobs []JobData
	start := 0
	firstCards := 0
	for page := 0; page < s.pagesToScrape; page++ {
		ReportProgress(ctx, Progress{
			Phase:         "page",
			Source:        string(db.SourceLinkedIn),
			Page:          page + 1,
			Pages:         s.pagesToScrape,
			Keywords:      query["keywords"],
			Location:      query["location"],
			JobsCollected: len(jobs),
			Message:       fmt.Sprintf("linkedin page %d/%d", page+1, s.pagesToScrape),
		})
		slog.Info("scraping linkedin page", "page", page+1, "of", s.pagesToScrape, "start", start)
		pageJobs, cards, err := s.scrapePage(ctx, s.buildSearchURL(query, start))
		if err != nil {
			return jobs, err
		}
		jobs = append(jobs, pageJobs...)
		if cards == 0 {
			break
		}
		start += cards
		if page == 0 {
			firstCards = cards
		} else if cards < firstCards {
			slog.Info("linkedin page short, stopping pagination",
				"page", page+1, "cards", cards, "first_page_cards", firstCards)
			break
		}
		if page < s.pagesToScrape-1 {
			if err := s.pause(ctx, linkedInPagePause); err != nil {
				return jobs, err
			}
		}
	}
	slog.Info("scraped linkedin jobs", "count", len(jobs))
	return jobs, nil
}

func (s *LinkedInScraper) scrapePage(ctx context.Context, pageURL string) ([]JobData, int, error) {
	resp, err := s.doGet(ctx, pageURL, "linkedin")
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	jobs, cards := s.transformJobCards(doc)
	return jobs, cards, nil
}

func (s *LinkedInScraper) transformJobCards(doc *html.Node) ([]JobData, int) {
	var jobs []JobData
	items := findAll(doc, "div", "base-search-card__info")
	for _, item := range items {
		title := text(findFirst(item, "h3", ""))
		if title == "" {
			title = "Unknown Title"
		}

		company := "Unknown Company"
		if c := findFirst(item, "a", "hidden-nested-link"); c != nil {
			company = strings.ReplaceAll(text(c), "\n", " ")
		}

		location := "Unknown Location"
		if l := findFirst(item, "span", "job-search-card__location"); l != nil {
			location = text(l)
		}

		jobURL := ""
		if item.Parent != nil {
			if urn := attr(item.Parent, "data-entity-urn"); urn != "" {
				parts := strings.Split(urn, ":")
				jobURL = "https://www.linkedin.com/jobs/view/" + parts[len(parts)-1] + "/"
			}
		}

		var date time.Time
		if t := findFirst(item, "time", ""); t != nil {
			if dt := attr(t, "datetime"); dt != "" {
				if parsed, err := time.Parse("2006-01-02", dt); err == nil {
					date = parsed
				}
			}
		}

		if jobURL == "" {
			continue // no stable URL means we cannot dedupe; skip
		}

		jobs = append(jobs, JobData{
			Title:    title,
			Company:  company,
			Location: location,
			JobURL:   jobURL,
			Date:     date,
			Source:   db.SourceLinkedIn,
		})
	}
	return jobs, len(items)
}

// ParseJobDescription extracts the posting body from LinkedIn job-detail HTML.
func ParseJobDescription(doc *html.Node) string {
	n := findFirst(doc, "div", "show-more-less-html__markup")
	return text(n)
}

// FetchJobDescription GETs job detail HTML and returns parsed text.
func (s *LinkedInScraper) FetchJobDescription(ctx context.Context, jobURL string) (string, error) {
	resp, err := s.doGet(ctx, jobURL, "linkedin detail")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return "", err
	}
	return ParseJobDescription(doc), nil
}

func (s *LinkedInScraper) doGet(ctx context.Context, rawURL, errPrefix string) (*http.Response, error) {
	attempts := linkedInRetryAttempts
	if attempts < 1 {
		attempts = 1
	}
	backoff := linkedInRetryInitial
	for attempt := 1; attempt <= attempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", linkedInUserAgent)

		resp, err := s.client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}

		status := resp.StatusCode
		header := resp.Header.Clone()
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		if !linkedInRetryable(status) || attempt == attempts {
			return nil, fmt.Errorf("%s returned status %d", errPrefix, status)
		}

		wait := linkedInRetryWait(header, backoff)
		slog.Warn("linkedin rate limited, retrying",
			"status", status,
			"attempt", attempt,
			"of", attempts,
			"wait", wait,
		)
		if err := s.pauseHeartbeat(ctx, wait, Progress{
			Phase:   "rate_limit",
			Source:  string(db.SourceLinkedIn),
			Message: fmt.Sprintf("linkedin %d, retry %d/%d in %s", status, attempt, attempts, wait),
		}); err != nil {
			return nil, err
		}
		if backoff > 0 {
			backoff *= 2
			if linkedInRetryMaxWait > 0 && backoff > linkedInRetryMaxWait {
				backoff = linkedInRetryMaxWait
			}
		}
	}
	return nil, fmt.Errorf("%s returned status 429", errPrefix)
}

func linkedInRetryable(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
}

func linkedInRetryWait(header http.Header, backoff time.Duration) time.Duration {
	wait := backoff
	raw := strings.TrimSpace(header.Get("Retry-After"))
	if raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			wait = time.Duration(secs) * time.Second
		} else if at, err := http.ParseTime(raw); err == nil {
			if until := time.Until(at); until > 0 {
				wait = until
			}
		}
	}
	if linkedInRetryMaxWait > 0 && wait > linkedInRetryMaxWait {
		wait = linkedInRetryMaxWait
	}
	if wait < 0 {
		return 0
	}
	return wait
}

func (s *LinkedInScraper) pause(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	if s.sleep != nil {
		return s.sleep(ctx, d)
	}
	return sleep(ctx, d)
}

func (s *LinkedInScraper) pauseHeartbeat(ctx context.Context, d time.Duration, progress Progress) error {
	if d <= 0 {
		return nil
	}
	ReportProgress(ctx, progress)
	chunk := linkedInRetryHeartbeat
	if chunk <= 0 {
		chunk = d
	}
	remaining := d
	for remaining > 0 {
		wait := remaining
		if wait > chunk {
			wait = chunk
		}
		if err := s.pause(ctx, wait); err != nil {
			return err
		}
		remaining -= wait
		if remaining > 0 {
			ReportProgress(ctx, progress)
		}
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
