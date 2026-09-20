package scraper

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jobscout/jobscout/internal/db"
	"golang.org/x/net/html"
)

// DiceScraper starts one Apify Dice actor run per search query.
type DiceScraper struct {
	settings    db.ScraperSettings
	client      *ApifyClient
	budgetCents int
	AroundStart func(ctx context.Context, fn func() error) error
}

func NewDice(settings db.ScraperSettings, client *ApifyClient, budgetCents int) *DiceScraper {
	pages := settings.PagesToScrape
	if pages < 1 {
		pages = 1
	}
	settings.PagesToScrape = pages
	return &DiceScraper{settings: settings, client: client, budgetCents: budgetCents}
}

func (s *DiceScraper) Source() db.JobSource { return db.SourceDice }

func (s *DiceScraper) ScrapeJobs(ctx context.Context, query map[string]string) (jobs []JobData, err error) {
	if s.client == nil {
		return nil, fmt.Errorf("apify client is required")
	}
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
		includeRemote := strings.EqualFold(strings.TrimSpace(query["include_remote"]), "true")
		run, err = s.client.startRun(ctx, ApifyDiceActorID, snap.RemainingCents, diceActorInput{
			Keyword:       query["keywords"],
			Location:      query["location"],
			PostedDate:    s.settings.TimespanCode,
			IncludeRemote: includeRemote,
			MaxPages:      s.settings.PagesToScrape,
		})
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
		return nil, fmt.Errorf("apify dice run %s status %s", run.ID, polled.Status)
	}
	if !isTerminalSuccess(polled.Status) {
		return nil, fmt.Errorf("apify dice run %s has unknown status %q", run.ID, polled.Status)
	}

	items, err := s.client.datasetItems(ctx, polled.DefaultDatasetID)
	if err != nil {
		return nil, err
	}
	scrapedAt := s.client.now()
	for _, item := range items {
		job, ok := MapDiceItem(item, query["location"], scrapedAt)
		if !ok {
			continue
		}
		jobs = append(jobs, job)
	}
	if _, waitErr := s.client.waitUsage(ctx, run.ID); waitErr != nil {
		return jobs, waitErr
	}
	return jobs, nil
}

// MapDiceItem maps one actor dataset row onto JobData. ok is false when required fields are missing.
func MapDiceItem(item map[string]any, searchLocation string, scrapedAt time.Time) (JobData, bool) {
	title := stringField(item, "title")
	company := stringField(item, "company", "companyName")
	rawURL := stringField(item, "url", "detailsPageUrl")
	jobURL, ok := canonicalizeDiceURL(rawURL)
	if !ok || title == "" || company == "" {
		return JobData{}, false
	}
	location := stringField(item, "location")
	if location == "" {
		location = stringField(item, "jobLocation")
	}
	if location == "" {
		location = searchLocation
	}
	desc := stringField(item, "description_text")
	if desc == "" {
		desc = htmlToText(stringField(item, "description_html"))
	}
	posted := stringField(item, "posted")
	date := parseDicePosted(posted, scrapedAt)
	workSetting := stringField(item, "workSetting")
	remote := containsRemote(workSetting) || containsRemote(location)
	return JobData{
		Title:       title,
		Company:     company,
		Location:    location,
		JobURL:      jobURL,
		Description: desc,
		Date:        date,
		Source:      db.SourceDice,
		IsRemote:    remote,
	}, true
}

func stringField(item map[string]any, keys ...string) string {
	for _, key := range keys {
		switch v := item[key].(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		}
	}
	return ""
}

func canonicalizeDiceURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host != "dice.com" && !strings.HasSuffix(host, ".dice.com") {
		return "", false
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.RawFragment = ""
	return u.String(), true
}

func parseDicePosted(raw string, fallback time.Time) time.Time {
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

func containsRemote(s string) bool {
	return strings.Contains(strings.ToLower(s), "remote")
}

var blockTags = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true, "br": true,
	"div": true, "dl": true, "dt": true, "dd": true, "footer": true, "form": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"header": true, "hr": true, "li": true, "main": true, "nav": true, "ol": true,
	"p": true, "pre": true, "section": true, "table": true, "td": true, "th": true,
	"tr": true, "ul": true,
}

func htmlToText(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return collapseSpace(stripTags(raw))
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil {
			return
		}
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "script", "style", "noscript":
				return
			}
			if blockTags[strings.ToLower(n.Data)] {
				b.WriteByte(' ')
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && blockTags[strings.ToLower(n.Data)] {
			b.WriteByte(' ')
		}
	}
	walk(doc)
	return collapseSpace(b.String())
}

var tagRe = regexp.MustCompile(`<[^>]+>`)
var spaceRe = regexp.MustCompile(`\s+`)

func stripTags(s string) string {
	return tagRe.ReplaceAllString(s, " ")
}

func collapseSpace(s string) string {
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}
