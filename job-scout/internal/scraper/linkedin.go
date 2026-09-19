package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jobscout/jobscout/internal/db"
	"golang.org/x/net/html"
)

const linkedInUserAgent = "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/109.0.0.0 Safari/537.36"

const linkedInBaseURL = "https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search"

var linkedInPagePause = 2 * time.Second

// linkedInWorkTypeOrder is the natural-language order for work-type phrases.
// LinkedIn no longer honors f_WT; selected types are prepended to keywords.
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

func isRemoteOnlyWorkType(fWT string) bool {
	codes := parseLinkedInWorkTypes(fWT)
	return len(codes) == 1 && codes[0] == "2"
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

func (s *LinkedInScraper) buildSearchURL(query map[string]string) string {
	params := []string{}
	if kw := query["keywords"]; kw != "" {
		params = append(params, "keywords="+url.QueryEscape(effectiveLinkedInKeywords(kw, query["f_WT"])))
	}
	if loc := query["location"]; loc != "" {
		params = append(params, "geoId="+loc)
	}
	// f_WT is ignored by LinkedIn guest search; work type lives in keywords.
	params = append(params, "f_TPR="+s.timespanCode, "start=0")
	return linkedInBaseURL + "?" + strings.Join(params, "&")
}

func (s *LinkedInScraper) ScrapeJobs(ctx context.Context, query map[string]string) ([]JobData, error) {
	if query["keywords"] == "" || query["location"] == "" {
		return nil, fmt.Errorf("invalid search query: missing required fields")
	}

	var jobs []JobData
	for page := 0; page < s.pagesToScrape; page++ {
		slog.Info("scraping linkedin page", "page", page+1, "of", s.pagesToScrape)
		pageURL := strings.Replace(s.buildSearchURL(query), "start=0", fmt.Sprintf("start=%d", 25*page), 1)

		pageJobs, err := s.scrapePage(ctx, pageURL)
		if err != nil {
			return jobs, err
		}
		stampRemote(pageJobs, isRemoteOnlyWorkType(query["f_WT"]))
		jobs = append(jobs, pageJobs...)
		if len(pageJobs) == 0 {
			break
		}
		if page < s.pagesToScrape-1 {
			if err := sleep(ctx, linkedInPagePause); err != nil {
				return jobs, err
			}
		}
	}
	slog.Info("scraped linkedin jobs", "count", len(jobs))
	return jobs, nil
}

func (s *LinkedInScraper) ScrapeHardcodedURL(ctx context.Context, cfg map[string]any) ([]JobData, error) {
	base, _ := cfg["url"].(string)
	if base == "" {
		return nil, fmt.Errorf("hardcoded url config missing 'url'")
	}

	remote, _ := cfg["is_remote"].(bool)
	if strings.Contains(base, "f_WT=2") {
		remote = true
	}

	var jobs []JobData
	for page := 0; page < s.pagesToScrape; page++ {
		pageURL := base
		if page > 0 {
			if strings.Contains(pageURL, "start=0") {
				pageURL = strings.Replace(pageURL, "start=0", fmt.Sprintf("start=%d", 25*page), 1)
			} else {
				pageURL = fmt.Sprintf("%s&start=%d", pageURL, 25*page)
			}
		}
		pageJobs, err := s.scrapePage(ctx, pageURL)
		if err != nil {
			return jobs, err
		}
		stampRemote(pageJobs, remote)
		jobs = append(jobs, pageJobs...)
		if len(pageJobs) == 0 {
			break
		}
		if page < s.pagesToScrape-1 {
			if err := sleep(ctx, linkedInPagePause); err != nil {
				return jobs, err
			}
		}
	}
	return jobs, nil
}

func (s *LinkedInScraper) scrapePage(ctx context.Context, pageURL string) ([]JobData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", linkedInUserAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("linkedin returned status %d", resp.StatusCode)
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, err
	}
	return s.transformJobCards(doc), nil
}

func (s *LinkedInScraper) transformJobCards(doc *html.Node) []JobData {
	var jobs []JobData
	for _, item := range findAll(doc, "div", "base-search-card__info") {
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
	return jobs
}

func stampRemote(jobs []JobData, remote bool) {
	if !remote {
		return
	}
	for i := range jobs {
		jobs[i].IsRemote = true
	}
}

// ParseJobDescription extracts the posting body from LinkedIn job-detail HTML.
func ParseJobDescription(doc *html.Node) string {
	n := findFirst(doc, "div", "show-more-less-html__markup")
	return text(n)
}

// FetchJobDescription GETs job detail HTML and returns parsed text.
func (s *LinkedInScraper) FetchJobDescription(ctx context.Context, jobURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jobURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", linkedInUserAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("linkedin detail returned status %d", resp.StatusCode)
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return "", err
	}
	return ParseJobDescription(doc), nil
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
