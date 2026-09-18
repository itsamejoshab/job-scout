package scraper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/db"
	"golang.org/x/net/html"
)

func TestLinkedIn_ParseSearchCardFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "linkedin_search_cards.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doc, err := html.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse fixture html: %v", err)
	}

	s := NewLinkedIn(db.ScraperSettings{})
	jobs := s.transformJobCards(doc)
	if len(jobs) != 2 {
		t.Fatalf("parsed jobs = %d, want 2 (skip cards with no entity URN)", len(jobs))
	}

	if jobs[0].Title != "IT Help Desk" {
		t.Errorf("job 0 title = %q, want IT Help Desk", jobs[0].Title)
	}
	if jobs[0].Company != "Acme Corp" {
		t.Errorf("job 0 company = %q, want Acme Corp", jobs[0].Company)
	}
	if jobs[0].Location != "New York, United States" {
		t.Errorf("job 0 location = %q, want New York, United States", jobs[0].Location)
	}
	if jobs[0].JobURL != "https://www.linkedin.com/jobs/view/4123456789/" {
		t.Errorf("job 0 url = %q, want guest-card view URL from data-entity-urn", jobs[0].JobURL)
	}
	if jobs[0].Source != db.SourceLinkedIn {
		t.Errorf("job 0 source = %q, want LINKEDIN", jobs[0].Source)
	}
	wantDate := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	if !jobs[0].Date.Equal(wantDate) {
		t.Errorf("job 0 date = %s, want %s", jobs[0].Date, wantDate)
	}

	if jobs[1].Title != "Application Support Analyst" {
		t.Errorf("job 1 title = %q, want Application Support Analyst", jobs[1].Title)
	}
	if jobs[1].Company != "Globex" {
		t.Errorf("job 1 company = %q, want Globex", jobs[1].Company)
	}
	if jobs[1].JobURL != "https://www.linkedin.com/jobs/view/4987654321/" {
		t.Errorf("job 1 url = %q, want https://www.linkedin.com/jobs/view/4987654321/", jobs[1].JobURL)
	}
}

func TestLinkedIn_GuestSearchURLUsesSeeMoreAPIAndFPP(t *testing.T) {
	s := NewLinkedIn(db.ScraperSettings{TimespanCode: "r84600"})
	got := s.buildSearchURL(map[string]string{
		"keywords": "IT Help Desk",
		"location": "101076143",
		"f_WT":     "2",
	})
	if !strings.Contains(got, "https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search?") {
		t.Errorf("search URL must keep the guest see-more API, got %q", got)
	}
	if !strings.Contains(got, "keywords=IT+Help+Desk") && !strings.Contains(got, "keywords=IT%20Help%20Desk") {
		t.Errorf("search URL must keep current keywords, got %q", got)
	}
	if !strings.Contains(got, "f_PP=101076143") {
		t.Errorf("search URL must keep f_PP from the seed location, got %q", got)
	}
	if !strings.Contains(got, "f_WT=2") {
		t.Errorf("search URL must keep f_WT=2 for remote queries, got %q", got)
	}
	if !strings.Contains(got, "f_TPR=r84600") {
		t.Errorf("search URL must keep timespan f_TPR, got %q", got)
	}
}

func TestNewLinkedIn_HTTPTimeoutIsExplicitNonZero(t *testing.T) {
	s := NewLinkedIn(db.ScraperSettings{})
	if s.client == nil || s.client.Timeout == 0 {
		t.Fatal("LinkedIn HTTP timeout must be explicit and non-zero")
	}
	if s.client.Timeout != 30*time.Second {
		t.Errorf("LinkedIn HTTP timeout = %s, want 30s default (HTTP_TIMEOUT_SECONDS)", s.client.Timeout)
	}
}

func TestNewLinkedInWithTimeout_UsesConfiguredDuration(t *testing.T) {
	s := NewLinkedInWithTimeout(db.ScraperSettings{}, 7*time.Second)
	if s.client == nil {
		t.Fatal("LinkedIn HTTP client is required")
	}
	if s.client.Timeout != 7*time.Second {
		t.Errorf("LinkedIn HTTP timeout = %s, want 7s from HTTP_TIMEOUT_SECONDS", s.client.Timeout)
	}
}
