package scraper

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/db"
)

func TestReportProgress_NoopWithoutReporter(t *testing.T) {
	ReportProgress(context.Background(), Progress{Phase: "query"})
}

func TestReportProgress_InvokesReporter(t *testing.T) {
	var got []Progress
	ctx := WithProgressReporter(context.Background(), func(p Progress) {
		got = append(got, p)
	})
	ReportProgress(ctx, Progress{Phase: "query", QueryIndex: 1, QueryCount: 2})
	if len(got) != 1 || got[0].Phase != "query" || got[0].QueryIndex != 1 || got[0].QueryCount != 2 {
		t.Fatalf("got %#v", got)
	}
}

func TestDiceScrapeJobs_ReportsPollProgress(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	fake.pollStatus = []string{"RUNNING", "RUNNING", "SUCCEEDED"}
	srv := fake.server()
	defer srv.Close()

	var mu sync.Mutex
	var phases []string
	ctx := WithProgressReporter(context.Background(), func(p Progress) {
		mu.Lock()
		defer mu.Unlock()
		phases = append(phases, p.Phase)
	})

	scraper := NewDice(db.ScraperSettings{TimespanCode: "24h", PagesToScrape: 1}, newTestApifyClient(srv.URL, "test-token", clock), 100)
	_, err := scraper.ScrapeJobs(ctx, map[string]string{
		"keywords": "x", "location": "y", "include_remote": "false",
	})
	if err != nil {
		t.Fatalf("ScrapeJobs: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	pollCount := 0
	for _, phase := range phases {
		if phase == "apify_poll" {
			pollCount++
		}
	}
	if pollCount < 3 {
		t.Fatalf("apify_poll heartbeats = %d phases=%#v, want at least 3", pollCount, phases)
	}
}

func TestIndeedScrapeJobs_RecordsAPIContractsOnContext(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	fake := newApifyFake(t)
	fake.items = []map[string]any{
		{
			"title": "Desktop Support", "companyName": "Acme",
			"jobUrl": "https://www.indeed.com/viewjob?jk=abc", "descriptionText": "Support",
		},
		{"title": "No URL", "companyName": "Skip"},
	}
	srv := fake.server()
	defer srv.Close()

	ctx := WithContractRecorder(context.Background())
	scraper := NewIndeed(
		db.ScraperSettings{ProviderOptions: db.IndeedOptionsMap(db.DefaultIndeedOptions())},
		newTestApifyClient(srv.URL, "test-token", clock),
		100,
	)
	jobs, err := scraper.ScrapeJobs(ctx, map[string]string{
		"keywords": "Desktop", "location": "Port Orange, FL",
		"include_remote": "false", "include_hybrid": "false", "radius": "15",
	})
	if err != nil {
		t.Fatalf("ScrapeJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1 mapped", len(jobs))
	}

	res := attachResultMetadata(ctx, Result{Status: "success", JobSource: "INDEED"})
	if res.Metadata == nil || len(res.Metadata.APICalls) < 2 {
		t.Fatalf("metadata api_calls = %#v, want start + dataset", res.Metadata)
	}
	var sawStart, sawDataset bool
	for _, call := range res.Metadata.APICalls {
		switch call.Phase {
		case "apify_start":
			sawStart = true
			if call.Request["query"] != "Desktop" {
				t.Errorf("start request query = %#v", call.Request["query"])
			}
			if call.ActorID != ApifyIndeedActorID {
				t.Errorf("actor = %q", call.ActorID)
			}
			if call.Path == "" || call.StatusCode == 0 {
				t.Errorf("start contract incomplete: %#v", call)
			}
		case "apify_dataset":
			sawDataset = true
			if call.ItemCount != 2 || call.MappedCount != 1 || call.SkippedCount != 1 {
				t.Errorf("dataset counts item=%d mapped=%d skipped=%d", call.ItemCount, call.MappedCount, call.SkippedCount)
			}
			if len(call.SampleKeys) == 0 || call.SampleItem == nil {
				t.Errorf("dataset sample missing: %#v", call)
			}
		}
	}
	if !sawStart || !sawDataset {
		t.Fatalf("missing phases in %#v", res.Metadata.APICalls)
	}
}

func TestCombineResults_MergesMetadata(t *testing.T) {
	got := CombineResults([]Result{
		{
			Status: "success", JobSource: "INDEED",
			Metadata: &ResultMetadata{APICalls: []APIContract{{Phase: "apify_start", ActorID: "indeed"}}},
		},
		{
			Status: "success", JobSource: "DICE",
			Metadata: &ResultMetadata{APICalls: []APIContract{{Phase: "apify_start", ActorID: "dice"}}},
		},
	})
	if got.Metadata == nil || len(got.Metadata.APICalls) != 2 {
		t.Fatalf("combined metadata = %#v", got.Metadata)
	}
}
