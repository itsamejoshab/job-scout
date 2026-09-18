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

func (s *LinkedInScraper) buildSearchURL(query map[string]string) string {
	params := []string{}
	if kw := query["keywords"]; kw != "" {
		params = append(params, "keywords="+url.QueryEscape(kw))
	}
	if loc := query["location"]; loc != "" {
		params = append(params, "f_PP="+loc)
	}
	if wt := query["f_WT"]; wt != "" {
		params = append(params, "f_WT="+wt)
	}
	params = append(params, "geoId=", "f_TPR="+s.timespanCode, "start=0")
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
		stampRemote(pageJobs, query["f_WT"] == "2")
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
