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
	jobs, cards := s.transformJobCards(doc)
	if len(jobs) != 2 {
		t.Fatalf("parsed jobs = %d, want 2 (skip cards with no entity URN)", len(jobs))
	}
	if cards != 3 {
		t.Errorf("cards = %d, want 3 including the no-URN card", cards)
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

func TestLinkedIn_GuestSearchURLUsesSeeMoreAPIAndGeoId(t *testing.T) {
	s := NewLinkedIn(db.ScraperSettings{TimespanCode: "r84600"})
	got := s.buildSearchURL(map[string]string{
		"keywords": "IT Help Desk",
		"location": "101076143",
		"f_WT":     "2",
	}, 0)
	if !strings.Contains(got, "https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search?") {
		t.Errorf("search URL must keep the guest see-more API, got %q", got)
	}
	if !strings.Contains(got, "keywords=Remote+IT+Help+Desk") && !strings.Contains(got, "keywords=Remote%20IT%20Help%20Desk") {
		t.Errorf("search URL must prepend Remote to keywords, got %q", got)
	}
	if !strings.Contains(got, "geoId=101076143") {
		t.Errorf("search URL must send seed location as geoId, got %q", got)
	}
	if strings.Contains(got, "f_PP=") {
		t.Errorf("search URL must not send f_PP; guest search ignores it, got %q", got)
	}
	if strings.Contains(got, "geoId=&") || strings.HasSuffix(got, "geoId=") {
		t.Errorf("search URL must not send an empty geoId, got %q", got)
	}
	if strings.Contains(got, "f_WT=") {
		t.Errorf("search URL must not send f_WT; work type is natural-language keywords, got %q", got)
	}
	if !strings.Contains(got, "f_TPR=r84600") {
		t.Errorf("search URL must keep timespan f_TPR, got %q", got)
	}
	if !strings.Contains(got, "start=0") {
		t.Errorf("first page search URL must send start=0, got %q", got)
	}
	later := s.buildSearchURL(map[string]string{
		"keywords": "IT Help Desk",
		"location": "101076143",
		"f_WT":     "2",
	}, 20)
	if !strings.Contains(later, "start=20") {
		t.Errorf("next page must use the observed card count as start, got %q", later)
	}
}

func TestLinkedIn_GuestSearchURLCombinesWorkTypesInKeywords(t *testing.T) {
	s := NewLinkedIn(db.ScraperSettings{TimespanCode: "r84600"})
	got := s.buildSearchURL(map[string]string{
		"keywords": "IT Help Desk",
		"location": "105135351",
		"f_WT":     "3,2",
	}, 0)
	if !strings.Contains(got, "keywords=Remote+or+Hybrid+IT+Help+Desk") &&
		!strings.Contains(got, "keywords=Remote%20or%20Hybrid%20IT%20Help%20Desk") {
		t.Errorf("search URL must combine selected work types into keywords, got %q", got)
	}
	if strings.Contains(got, "f_WT=") {
		t.Errorf("search URL must omit f_WT, got %q", got)
	}

	unrestricted := s.buildSearchURL(map[string]string{
		"keywords": "IT Help Desk",
		"location": "101076143",
		"f_WT":     "",
	}, 0)
	if !strings.Contains(unrestricted, "keywords=IT+Help+Desk") &&
		!strings.Contains(unrestricted, "keywords=IT%20Help%20Desk") {
		t.Errorf("empty f_WT must leave keywords unchanged, got %q", unrestricted)
	}
}

func TestLinkedIn_GuestSearchURLOmitsGeoIdForGlobalSearches(t *testing.T) {
	s := NewLinkedIn(db.ScraperSettings{TimespanCode: "r84600"})
	got := s.buildSearchURL(map[string]string{
		"keywords": "help desk or IT support jobs that are onsite near port orange, FL or hybrid if more than 10 miles, remote only if more than 40 miles",
	}, 0)
	if !strings.Contains(got, "keywords=") {
		t.Errorf("global search must send natural-language keywords, got %q", got)
	}
	if !strings.Contains(got, "help") || !strings.Contains(got, "port") {
		t.Errorf("global search keywords missing expected terms, got %q", got)
	}
	if strings.Contains(got, "geoId=") {
		t.Errorf("global search must omit geoId, got %q", got)
	}
	if strings.Contains(got, "f_WT=") {
		t.Errorf("search URL must not send f_WT, got %q", got)
	}
	if !strings.Contains(got, "f_TPR=r84600") {
		t.Errorf("global search must still send timespan f_TPR, got %q", got)
	}
}

func TestCombineLinkedInSearchQueries_MergesLegacyRows(t *testing.T) {
	got := combineLinkedInSearchQueries([]map[string]string{
		{"keywords": "IT Help Desk", "location": "105135351", "f_WT": "3"},
		{"keywords": "IT Help Desk", "location": "105135351", "f_WT": "2"},
		{"keywords": "IT Help Desk", "location": "101076143", "f_WT": ""},
		{"keywords": "Application Support", "location": "101076143", "f_WT": "2"},
	})
	want := []map[string]string{
		{"keywords": "IT Help Desk", "location": "105135351", "f_WT": "2,3"},
		{"keywords": "IT Help Desk", "location": "101076143", "f_WT": ""},
		{"keywords": "Application Support", "location": "101076143", "f_WT": "2"},
	}
	if len(got) != len(want) {
		t.Fatalf("combineLinkedInSearchQueries len = %d, want %d (%#v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i]["keywords"] != want[i]["keywords"] ||
			got[i]["location"] != want[i]["location"] ||
			got[i]["f_WT"] != want[i]["f_WT"] {
			t.Errorf("row %d = %#v, want %#v", i, got[i], want[i])
		}
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
