package scraper

import (
	"strings"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/db"
)

func TestIndeedScrapeJobs_HTTPContract(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	fake.listedRuns = []map[string]any{
		{"id": "prior", "status": "SUCCEEDED", "usageTotalUsd": 0.25, "startedAt": "2026-09-21T01:00:00.000Z"},
	}
	fake.items = []map[string]any{
		{
			"title": "Desktop Support", "companyName": "Acme",
			"location":        map[string]any{"city": "Daytona Beach", "admin1Code": "FL", "countryCode": "US"},
			"jobUrl":          "https://www.indeed.com/viewjob?jk=abc&utm=1#frag",
			"descriptionText": "Support desktops.", "datePublished": "2026-09-18",
			"isRemote": false,
		},
		{
			"title": "Remote Endpoint", "companyName": "Globex",
			"jobUrl":            "https://www.indeed.com/viewjob?jk=def",
			"descriptionHtml":   "<div>Help <script>x()</script><p>users</p></div>",
			"datePublished":     "2026-09-19",
			"isRemote":          true,
			"formattedLocation": "Remote",
		},
		{"title": "No URL", "companyName": "Skip Co"},
		{"title": "Bad host", "companyName": "Skip Co", "jobUrl": "https://example.com/job/1"},
		{"companyName": "No title", "jobUrl": "https://www.indeed.com/viewjob?jk=no-title"},
	}
	srv := fake.server()
	defer srv.Close()

	settings := db.ScraperSettings{
		ProviderOptions: db.IndeedOptionsMap(db.DefaultIndeedOptions()),
	}
	scraper := NewIndeed(settings, newTestApifyClient(srv.URL, "test-token", clock), 100)
	jobs, err := scraper.ScrapeJobs(t.Context(), map[string]string{
		"keywords":       "Desktop Support",
		"location":       "Port Orange, FL",
		"include_remote": "false",
		"include_hybrid": "false",
		"radius":         "25",
	})
	if err != nil {
		t.Fatalf("ScrapeJobs: %v", err)
	}
	if fake.startCount.Load() != 1 {
		t.Errorf("start POSTs = %d, want 1", fake.startCount.Load())
	}
	if !strings.Contains(fake.lastStart.path, "indeed-scraper") {
		t.Errorf("start path = %q, want borderline~indeed-scraper", fake.lastStart.path)
	}
	if fake.lastStart.body["query"] != "Desktop Support" {
		t.Errorf("query = %#v", fake.lastStart.body["query"])
	}
	if fake.lastStart.body["country"] != "us" {
		t.Errorf("country = %#v", fake.lastStart.body["country"])
	}
	if fake.lastStart.body["location"] != "Port Orange, FL" {
		t.Errorf("location = %#v", fake.lastStart.body["location"])
	}
	if fake.lastStart.body["radius"] != "25" {
		t.Errorf("radius = %#v, want per-location 25", fake.lastStart.body["radius"])
	}
	if fake.lastStart.body["jobType"] != "fulltime" {
		t.Errorf("jobType = %#v", fake.lastStart.body["jobType"])
	}
	if fake.lastStart.body["fromDays"] != "1" {
		t.Errorf("fromDays = %#v", fake.lastStart.body["fromDays"])
	}
	if _, ok := fake.lastStart.body["remote"]; ok {
		t.Errorf("remote must be omitted when both checkboxes are off, body=%v", fake.lastStart.body)
	}
	if fake.lastStart.body["enableUniqueJobs"] != true {
		t.Errorf("enableUniqueJobs = %#v, want true", fake.lastStart.body["enableUniqueJobs"])
	}
	if fake.lastStart.body["includeSimilarJobs"] != false {
		t.Errorf("includeSimilarJobs = %#v, want false", fake.lastStart.body["includeSimilarJobs"])
	}
	if maxRows, _ := fake.lastStart.body["maxRows"].(float64); maxRows != 100 {
		t.Errorf("maxRows = %#v, want 100", fake.lastStart.body["maxRows"])
	}
	if len(jobs) != 2 {
		t.Fatalf("mapped jobs = %d, want 2", len(jobs))
	}
	if jobs[0].JobURL != "https://www.indeed.com/viewjob?jk=abc" {
		t.Errorf("canonical url = %q", jobs[0].JobURL)
	}
	if jobs[0].Source != db.SourceIndeed {
		t.Errorf("source = %q, want INDEED", jobs[0].Source)
	}
	if jobs[0].Location != "Daytona Beach, FL, US" {
		t.Errorf("location = %q, want nested city/state/country", jobs[0].Location)
	}
	if jobs[0].IsRemote {
		t.Error("onsite job must not set is_remote")
	}
	if jobs[1].IsRemote {
		// ok
	} else {
		t.Error("isRemote true must set IsRemote")
	}
	if !strings.Contains(jobs[1].Description, "Help") || strings.Contains(jobs[1].Description, "script") {
		t.Errorf("html description = %q", jobs[1].Description)
	}
}

func TestIndeedScrapeJobs_RemoteAndHybridFilters(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}

	t.Run("remote_only", func(t *testing.T) {
		fake := newApifyFake(t)
		fake.items = []map[string]any{{
			"title": "A", "companyName": "B", "jobUrl": "https://www.indeed.com/viewjob?jk=r1",
			"descriptionText": "x",
		}}
		srv := fake.server()
		defer srv.Close()
		scraper := NewIndeed(db.ScraperSettings{ProviderOptions: db.IndeedOptionsMap(db.DefaultIndeedOptions())},
			newTestApifyClient(srv.URL, "test-token", clock), 100)
		if _, err := scraper.ScrapeJobs(t.Context(), map[string]string{
			"keywords": "x", "location": "y", "include_remote": "true", "include_hybrid": "false",
		}); err != nil {
			t.Fatalf("ScrapeJobs: %v", err)
		}
		if fake.startCount.Load() != 1 {
			t.Fatalf("starts = %d, want 1", fake.startCount.Load())
		}
		if fake.lastStart.body["remote"] != "remote" {
			t.Errorf("remote = %#v, want remote", fake.lastStart.body["remote"])
		}
	})

	t.Run("both_checked", func(t *testing.T) {
		fake := newApifyFake(t)
		fake.items = []map[string]any{{
			"title": "A", "companyName": "B", "jobUrl": "https://www.indeed.com/viewjob?jk=both",
			"descriptionText": "x",
		}}
		srv := fake.server()
		defer srv.Close()
		scraper := NewIndeed(db.ScraperSettings{ProviderOptions: db.IndeedOptionsMap(db.DefaultIndeedOptions())},
			newTestApifyClient(srv.URL, "test-token", clock), 100)
		if _, err := scraper.ScrapeJobs(t.Context(), map[string]string{
			"keywords": "x", "location": "y", "include_remote": "true", "include_hybrid": "true",
		}); err != nil {
			t.Fatalf("ScrapeJobs: %v", err)
		}
		if fake.startCount.Load() != 2 {
			t.Fatalf("starts = %d, want 2 for remote+hybrid", fake.startCount.Load())
		}
	})
}

func TestMapIndeedItem_AliasesCanonicalURLRemoteAndDates(t *testing.T) {
	scraped := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	t.Run("skip_unusable", func(t *testing.T) {
		cases := []map[string]any{
			{"title": "A", "companyName": "B"},
			{"title": "A", "jobUrl": "https://www.indeed.com/viewjob?jk=x"},
			{"companyName": "B", "jobUrl": "https://www.indeed.com/viewjob?jk=x"},
			{"title": "A", "companyName": "B", "jobUrl": "ftp://indeed.com/job"},
			{"title": "A", "companyName": "B", "jobUrl": "https://notindeed.com/job"},
		}
		for i, item := range cases {
			if _, ok := MapIndeedItem(item, "Port Orange, FL", scraped); ok {
				t.Errorf("case %d mapped, want skip: %#v", i, item)
			}
		}
	})
	t.Run("date_and_fallback_location", func(t *testing.T) {
		job, ok := MapIndeedItem(map[string]any{
			"title": "A", "companyName": "B",
			"jobUrl": "https://www.indeed.com/viewjob?jk=d", "datePublished": "2026-09-18",
		}, "Port Orange, FL", scraped)
		if !ok {
			t.Fatal("wanted map")
		}
		want := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
		if !job.Date.Equal(want) {
			t.Errorf("date = %s, want %s", job.Date, want)
		}
		if job.Location != "Port Orange, FL" {
			t.Errorf("location = %q, want search fallback", job.Location)
		}
	})
}

func TestIndeedRemoteFilters(t *testing.T) {
	cases := []struct {
		remote, hybrid string
		want           []string
	}{
		{"false", "false", []string{""}},
		{"true", "false", []string{"remote"}},
		{"false", "true", []string{"hybrid"}},
		{"true", "true", []string{"remote", "hybrid"}},
	}
	for _, tc := range cases {
		got := indeedRemoteFilters(map[string]string{
			"include_remote": tc.remote, "include_hybrid": tc.hybrid,
		})
		if len(got) != len(tc.want) {
			t.Fatalf("remote=%s hybrid=%s got %#v want %#v", tc.remote, tc.hybrid, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("remote=%s hybrid=%s got %#v want %#v", tc.remote, tc.hybrid, got, tc.want)
			}
		}
	}
}
