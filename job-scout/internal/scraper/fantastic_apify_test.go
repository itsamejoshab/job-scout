package scraper

import (
	"strings"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/db"
)

func TestFantasticScrapeJobs_HTTPContract(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	fake.listedRuns = []map[string]any{
		{"id": "prior", "status": "SUCCEEDED", "usageTotalUsd": 0.25, "startedAt": "2026-09-21T01:00:00.000Z"},
	}
	fake.items = []map[string]any{
		{
			"title": "Desktop Support", "organization": "Acme",
			"url":                 "https://boards.greenhouse.io/acme/jobs/1#frag",
			"description_text":    "Support desktops.",
			"date_posted":         "2026-09-18T15:04:05Z",
			"locations_derived":   []any{"Daytona Beach, Florida, United States"},
			"ai_work_arrangement": "On-site",
		},
		{
			"title": "Remote Endpoint", "organization": "Globex",
			"url":                 "https://jobs.ashbyhq.com/globex/abc",
			"description_html":    "<div>Help <script>x()</script><p>users</p></div>",
			"date_posted":         "2026-09-19",
			"ai_work_arrangement": "Remote Solely",
			"location_type":       "TELECOMMUTE",
		},
		{"title": "No URL", "organization": "Skip Co"},
		{"title": "Bad scheme", "organization": "Skip Co", "url": "ftp://example.com/job/1"},
		{"organization": "No title", "url": "https://careers.example.com/jobs/no-title"},
	}
	srv := fake.server()
	defer srv.Close()

	settings := db.ScraperSettings{
		ProviderOptions: db.FantasticOptionsMap(db.DefaultFantasticOptions()),
	}
	scraper := NewFantastic(settings, newTestApifyClient(srv.URL, "test-token", clock), 100)
	jobs, err := scraper.ScrapeJobs(t.Context(), map[string]string{
		"query_index": "0",
		"keywords":    "Desktop Support, Application Support",
		"location":    "United States",
	})
	if err != nil {
		t.Fatalf("ScrapeJobs: %v", err)
	}
	if fake.startCount.Load() != 1 {
		t.Errorf("start POSTs = %d, want 1", fake.startCount.Load())
	}
	if !strings.Contains(fake.lastStart.path, "career-site-job-listing-feed") {
		t.Errorf("start path = %q, want fantastic-jobs~career-site-job-listing-feed", fake.lastStart.path)
	}
	if titles, ok := fake.lastStart.body["titleSearch"].([]any); !ok || len(titles) != 2 || titles[0] != "Desktop Support" {
		t.Errorf("titleSearch = %#v", fake.lastStart.body["titleSearch"])
	}
	if locations, ok := fake.lastStart.body["locationSearch"].([]any); !ok || len(locations) != 1 || locations[0] != "United States" {
		t.Errorf("locationSearch = %#v", fake.lastStart.body["locationSearch"])
	}
	if exclusions, ok := fake.lastStart.body["titleExclusionSearch"].([]any); !ok || len(exclusions) != 2 {
		t.Errorf("titleExclusionSearch = %#v", fake.lastStart.body["titleExclusionSearch"])
	}
	if locEx, ok := fake.lastStart.body["locationExclusionSearch"].([]any); !ok || len(locEx) != 2 || locEx[0] != "India:*" {
		t.Errorf("locationExclusionSearch = %#v", fake.lastStart.body["locationExclusionSearch"])
	}
	if work, ok := fake.lastStart.body["aiWorkArrangementFilter"].([]any); !ok || len(work) != 1 || work[0] != "Remote Solely" {
		t.Errorf("aiWorkArrangementFilter = %#v", fake.lastStart.body["aiWorkArrangementFilter"])
	}
	if emp, ok := fake.lastStart.body["aiEmploymentTypeFilter"].([]any); !ok || len(emp) != 1 || emp[0] != "FULL_TIME" {
		t.Errorf("aiEmploymentTypeFilter = %#v", fake.lastStart.body["aiEmploymentTypeFilter"])
	}
	if limit, _ := fake.lastStart.body["limit"].(float64); limit != 200 {
		t.Errorf("limit = %#v, want 200", fake.lastStart.body["limit"])
	}
	if fake.lastStart.body["descriptionType"] != "text" {
		t.Errorf("descriptionType = %#v", fake.lastStart.body["descriptionType"])
	}
	if fake.lastStart.body["removeAgency"] != true {
		t.Errorf("removeAgency = %#v, want true", fake.lastStart.body["removeAgency"])
	}
	if fake.lastStart.body["includeCompanyDetails"] != false {
		t.Errorf("includeCompanyDetails = %#v, want false", fake.lastStart.body["includeCompanyDetails"])
	}
	if fake.lastStart.body["remote only (legacy)"] != false {
		t.Errorf("remote only (legacy) = %#v, want false", fake.lastStart.body["remote only (legacy)"])
	}
	if len(jobs) != 2 {
		t.Fatalf("mapped jobs = %d, want 2", len(jobs))
	}
	if jobs[0].JobURL != "https://boards.greenhouse.io/acme/jobs/1" {
		t.Errorf("canonical url = %q", jobs[0].JobURL)
	}
	if jobs[0].Source != db.SourceFantastic {
		t.Errorf("source = %q, want FANTASTIC", jobs[0].Source)
	}
	if jobs[0].Company != "Acme" || jobs[0].Title != "Desktop Support" {
		t.Errorf("job 0 title/company = %q / %q", jobs[0].Title, jobs[0].Company)
	}
	if jobs[0].Location != "Daytona Beach, Florida, United States" {
		t.Errorf("location = %q, want locations_derived", jobs[0].Location)
	}
	if jobs[0].IsRemote {
		t.Error("On-site arrangement must not set is_remote")
	}
	if jobs[1].IsRemote != true {
		t.Error("Remote Solely must set is_remote")
	}
	if !strings.Contains(jobs[1].Description, "Help") || strings.Contains(jobs[1].Description, "script") {
		t.Errorf("html description = %q", jobs[1].Description)
	}
	if jobs[1].Location != "United States" {
		t.Errorf("empty locations_derived must fall back to search location, got %q", jobs[1].Location)
	}
}

func TestFantasticScrapeJobs_SecondQueryUsesOwnPayload(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	fake.items = []map[string]any{{
		"title": "A", "organization": "B", "url": "https://careers.example.com/jobs/2",
		"description_text": "x",
	}}
	srv := fake.server()
	defer srv.Close()

	second := db.DefaultFantasticQuery()
	second.TitleSearch = []string{"Endpoint Support"}
	second.LocationSearch = []string{"Canada"}
	second.AIWorkArrangementFilter = []string{"Hybrid"}
	second.Limit = 500
	settings := db.ScraperSettings{
		ProviderOptions: db.FantasticOptionsMap(db.FantasticProviderOptions{
			Queries: []db.FantasticQuery{db.DefaultFantasticQuery(), second},
		}),
	}
	scraper := NewFantastic(settings, newTestApifyClient(srv.URL, "test-token", clock), 100)
	if _, err := scraper.ScrapeJobs(t.Context(), map[string]string{"query_index": "1"}); err != nil {
		t.Fatalf("ScrapeJobs: %v", err)
	}
	if titles, ok := fake.lastStart.body["titleSearch"].([]any); !ok || len(titles) != 1 || titles[0] != "Endpoint Support" {
		t.Errorf("second query titleSearch = %#v", fake.lastStart.body["titleSearch"])
	}
	if locations, ok := fake.lastStart.body["locationSearch"].([]any); !ok || len(locations) != 1 || locations[0] != "Canada" {
		t.Errorf("second query locationSearch = %#v", fake.lastStart.body["locationSearch"])
	}
	if work, ok := fake.lastStart.body["aiWorkArrangementFilter"].([]any); !ok || len(work) != 1 || work[0] != "Hybrid" {
		t.Errorf("second query work = %#v", fake.lastStart.body["aiWorkArrangementFilter"])
	}
	if limit, _ := fake.lastStart.body["limit"].(float64); limit != 500 {
		t.Errorf("second query limit = %#v, want 500", fake.lastStart.body["limit"])
	}
}
