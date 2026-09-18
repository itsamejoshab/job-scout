package scraper

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/db"
	"golang.org/x/net/html"
)

func TestParseJobDescription_FixtureExtractsBody(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "linkedin_job_detail.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doc, err := html.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse fixture html: %v", err)
	}

	got := ParseJobDescription(doc)
	if !strings.Contains(got, "computer troubleshooting") {
		t.Errorf("description %q must include computer troubleshooting from the fixture", got)
	}
	if !strings.Contains(got, "windows desktops") {
		t.Errorf("description %q must include windows desktops from the fixture", got)
	}
	if strings.Contains(got, "show-more-less-html") {
		t.Errorf("description must be text content, not raw class names, got %q", got)
	}
	if strings.Contains(got, "Sidebar login widget") || strings.Contains(got, "PAGE CHROME") {
		t.Errorf("description must come from show-more-less-html__markup, not the whole page, got %q", got)
	}
}

func TestParseJobDescription_EmptyMarkupIsEmptyString(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "linkedin_job_detail_empty.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doc, err := html.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse fixture html: %v", err)
	}
	got := ParseJobDescription(doc)
	if strings.TrimSpace(got) != "" {
		t.Errorf("empty detail markup must parse to empty text, got %q", got)
	}
}

func TestFetchJobDescription_UsesHTTPTimeoutAndParsesBody(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "linkedin_job_detail.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Method != http.MethodGet {
			t.Errorf("detail fetch method = %s, want GET", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)

	s := NewLinkedInWithTimeout(db.ScraperSettings{}, 7*time.Second)
	if s.client == nil || s.client.Timeout != 7*time.Second {
		t.Fatal("LinkedIn detail GET must use the explicit HTTP timeout")
	}
	s.client = srv.Client()
	s.client.Timeout = 7 * time.Second

	got, err := s.FetchJobDescription(t.Context(), srv.URL+"/jobs/view/4123456789/")
	if err != nil {
		t.Fatalf("FetchJobDescription: %v", err)
	}
	if hits != 1 {
		t.Errorf("detail GET calls = %d, want 1", hits)
	}
	if !strings.Contains(got, "computer troubleshooting") {
		t.Errorf("fetched description %q must come from detail HTML", got)
	}
}

func TestFetchJobDescription_HonorsClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s := NewLinkedInWithTimeout(db.ScraperSettings{}, 50*time.Millisecond)
	s.client.Transport = srv.Client().Transport
	start := time.Now()
	_, err := s.FetchJobDescription(t.Context(), srv.URL+"/jobs/view/1/")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("detail GET must fail when the HTTP timeout elapses")
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("detail GET waited %s, want failure near the 50ms HTTP timeout", elapsed)
	}
}
