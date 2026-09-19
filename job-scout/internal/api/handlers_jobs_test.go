package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Run(m))
}

func TestJobs_ListFiltersOrdersPagesAndOmitsDescription(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	description := "Full private description"
	for _, job := range []db.Job{
		{
			JobSource:   db.SourceLinkedIn,
			Title:       "Platform Engineer",
			Company:     "Acme",
			Description: &description,
			Location:    "Remote",
			JobURL:      "https://example.test/jobs/one",
		},
		{
			JobSource: db.SourceLinkedIn,
			Title:     "Platform Engineer",
			Company:   "Acme",
			Location:  "Remote",
			JobURL:    "https://example.test/jobs/two",
		},
		{
			JobSource: db.SourceLinkedIn,
			Title:     "Platform Engineer",
			Company:   "Acme",
			Location:  "Remote",
			JobURL:    "https://example.test/jobs/three",
		},
		{
			JobSource: db.SourceIndeed,
			Title:     "Platform Engineer",
			Company:   "Acme",
			Location:  "Remote",
			JobURL:    "https://example.test/jobs/wrong-source",
		},
		{
			JobSource: db.SourceLinkedIn,
			Title:     "Accountant",
			Company:   "Other",
			Location:  "Remote",
			JobURL:    "https://example.test/jobs/wrong-search",
		},
	} {
		if _, err := db.InsertJobIfNew(t.Context(), pool, job); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	if _, err := pool.Exec(`
		UPDATE jobs SET created_at = '2026-09-18 04:30:00+00'
		WHERE job_url IN ('https://example.test/jobs/one', 'https://example.test/jobs/two');
		UPDATE jobs SET created_at = '2026-09-18 03:59:59+00'
		WHERE job_url = 'https://example.test/jobs/three';
		UPDATE jobs SET created_at = '2026-09-18 04:30:00+00'
		WHERE job_url IN (
			'https://example.test/jobs/wrong-source',
			'https://example.test/jobs/wrong-search'
		);
		UPDATE jobs SET state = 'ready'
	`); err != nil {
		t.Fatalf("prepare jobs: %v", err)
	}

	h := &Handler{DB: pool, Cfg: config.Config{ReportingTimezone: "America/New_York"}}
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v0/jobs?state=ready&job_source=linkedin&q=acme&date_from=2026-09-18&date_to=2026-09-18&limit=1",
		nil,
	)
	rec := httptest.NewRecorder()
	NewServer("", h).Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v0/jobs status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
		AsOf  string           `json:"as_of"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("jobs JSON: %v", err)
	}
	if len(page.Items) != 1 || page.Total != 2 || page.AsOf == "" {
		t.Fatalf("page = %#v, want one of two items and an anchor", page)
	}
	if page.Items[0]["state"] != "ready" {
		t.Errorf("state=%v, want ready", page.Items[0]["state"])
	}
	if page.Items[0]["job_url"] != "https://example.test/jobs/two" {
		t.Errorf("first tied item URL=%v, want higher id", page.Items[0]["job_url"])
	}
	if _, exists := page.Items[0]["description"]; exists {
		t.Error("list item must omit description")
	}
	if page.Items[0]["has_description"] != false {
		t.Errorf("has_description=%v, want false", page.Items[0]["has_description"])
	}
	if page.Items[0]["description_preview"] != "" {
		t.Errorf("description_preview=%v, want empty", page.Items[0]["description_preview"])
	}

	if _, err := pool.Exec(`
		INSERT INTO jobs (job_source, title, company, location, date, job_url, created_at)
		VALUES ('LINKEDIN', 'Platform Engineer', 'Acme', 'Remote', now(),
		        'https://example.test/jobs/new-after-anchor', now() + interval '1 minute')
	`); err != nil {
		t.Fatalf("insert after anchor: %v", err)
	}
	secondPath := "/api/v0/jobs?state=ready&job_source=linkedin&q=acme" +
		"&date_from=2026-09-18&date_to=2026-09-18&limit=1&offset=1&as_of=" +
		page.AsOf
	req = httptest.NewRequest(http.MethodGet, secondPath, nil)
	rec = httptest.NewRecorder()
	NewServer("", h).Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second page status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("second page JSON: %v", err)
	}
	if page.Total != 2 || len(page.Items) != 1 {
		t.Fatalf("pinned second page = %#v, want one of two original rows", page)
	}
	if page.Items[0]["job_url"] != "https://example.test/jobs/one" {
		t.Errorf("second tied item URL=%v, want lower id", page.Items[0]["job_url"])
	}
	if page.Items[0]["has_description"] != true {
		t.Errorf("has_description=%v, want true", page.Items[0]["has_description"])
	}
	if page.Items[0]["description_preview"] != "Full private description" {
		t.Errorf("description_preview=%v, want truncated preview text", page.Items[0]["description_preview"])
	}
	if _, exists := page.Items[0]["description"]; exists {
		t.Error("list item must omit full description")
	}
}

func TestJobs_LastHoursFilterIsRelativeToAsOf(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, job := range []db.Job{
		{
			JobSource: db.SourceLinkedIn,
			Title:     "Recent Engineer",
			Company:   "Acme",
			Location:  "Remote",
			JobURL:    "https://example.test/jobs/recent",
		},
		{
			JobSource: db.SourceLinkedIn,
			Title:     "Older Engineer",
			Company:   "Acme",
			Location:  "Remote",
			JobURL:    "https://example.test/jobs/older",
		},
	} {
		if _, err := db.InsertJobIfNew(t.Context(), pool, job); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	if _, err := pool.Exec(`
		UPDATE jobs SET created_at = '2026-09-18 20:00:00+00', state = 'ready'
		WHERE job_url = 'https://example.test/jobs/recent';
		UPDATE jobs SET created_at = '2026-09-17 18:00:00+00', state = 'ready'
		WHERE job_url = 'https://example.test/jobs/older';
	`); err != nil {
		t.Fatalf("prepare jobs: %v", err)
	}

	h := &Handler{DB: pool, Cfg: config.Config{ReportingTimezone: "America/New_York"}}
	asOf := "2026-09-18T21:00:00Z"
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v0/jobs?state=ready&last_hours=24&as_of="+asOf,
		nil,
	)
	rec := httptest.NewRecorder()
	NewServer("", h).Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v0/jobs status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("jobs JSON: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("page = %#v, want only the recent job", page)
	}
	if page.Items[0]["job_url"] != "https://example.test/jobs/recent" {
		t.Errorf("job_url=%v, want recent job", page.Items[0]["job_url"])
	}

	req = httptest.NewRequest(
		http.MethodGet,
		"/api/v0/jobs?last_hours=24&date_from=2026-09-18",
		nil,
	)
	rec = httptest.NewRecorder()
	NewServer("", h).Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("combined filters status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func TestJobs_DescriptionPreviewIsWhitespaceCollapsedAndTruncated(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	long := strings.Repeat("word ", 200)
	description := "  Line one.\n\nLine   two.  " + long
	if _, err := db.InsertJobIfNew(t.Context(), pool, db.Job{
		JobSource:   db.SourceLinkedIn,
		Title:       "Preview Engineer",
		Company:     "Acme",
		Description: &description,
		Location:    "Remote",
		JobURL:      "https://example.test/jobs/preview",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	h := &Handler{DB: pool, Cfg: config.Config{ReportingTimezone: "America/New_York"}}
	req := httptest.NewRequest(http.MethodGet, "/api/v0/jobs", nil)
	rec := httptest.NewRecorder()
	NewServer("", h).Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v0/jobs status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("jobs JSON: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items=%d, want 1", len(page.Items))
	}
	preview, _ := page.Items[0]["description_preview"].(string)
	if strings.Contains(preview, "\n") || strings.Contains(preview, "  ") {
		t.Errorf("preview must collapse whitespace, got %q", preview)
	}
	if !strings.HasPrefix(preview, "Line one. Line two.") {
		t.Errorf("preview=%q, want collapsed leading text", preview)
	}
	runes := []rune(preview)
	if len(runes) != 480 || !strings.HasSuffix(preview, "…") {
		t.Errorf("preview length=%d suffix=%q, want 480 chars ending in ellipsis", len(runes), preview[len(preview)-1:])
	}
	if _, exists := page.Items[0]["description"]; exists {
		t.Error("list item must omit full description")
	}
}

func TestJobs_DefaultLimitAndMaximumClamp(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(`
		INSERT INTO jobs (job_source, title, company, location, date, job_url)
		SELECT 'LINKEDIN', 'Job ' || n, 'Acme', 'Remote', now(),
		       'https://example.test/jobs/limit-' || n
		FROM generate_series(1, 201) AS n
	`); err != nil {
		t.Fatalf("insert jobs: %v", err)
	}
	h := &Handler{DB: pool}
	for _, tc := range []struct {
		path string
		want int
	}{
		{path: "/api/v0/jobs", want: 50},
		{path: "/api/v0/jobs?limit=999", want: 200},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		NewServer("", h).Handler.ServeHTTP(rec, req)
		var page struct {
			Items []map[string]any `json:"items"`
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", tc.path, rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatalf("%s JSON: %v", tc.path, err)
		}
		if len(page.Items) != tc.want {
			t.Errorf("%s items=%d, want %d", tc.path, len(page.Items), tc.want)
		}
	}
}

func TestJobByIDReturnsFullRowAndValidatesID(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	description := "Full description"
	if _, err := db.InsertJobIfNew(t.Context(), pool, db.Job{
		JobSource: db.SourceLinkedIn, Title: "Engineer", Company: "Acme",
		Description: &description, Location: "Remote", JobURL: "https://example.test/jobs/detail",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var id int64
	if err := pool.QueryRow(`SELECT id FROM jobs WHERE job_url = 'https://example.test/jobs/detail'`).Scan(&id); err != nil {
		t.Fatalf("select id: %v", err)
	}

	h := &Handler{DB: pool}
	for _, tc := range []struct {
		path       string
		wantStatus int
	}{
		{path: fmt.Sprintf("/api/v0/jobs/%d", id), wantStatus: http.StatusOK},
		{path: "/api/v0/jobs/999999", wantStatus: http.StatusNotFound},
		{path: "/api/v0/jobs/not-a-number", wantStatus: http.StatusBadRequest},
		{path: "/api/v0/jobs/stats", wantStatus: http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		NewServer("", h).Handler.ServeHTTP(rec, req)
		if rec.Code != tc.wantStatus {
			t.Errorf("%s status=%d body=%s, want %d", tc.path, rec.Code, rec.Body.String(), tc.wantStatus)
		}
		if tc.path == fmt.Sprintf("/api/v0/jobs/%d", id) {
			var job map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
				t.Fatalf("detail JSON: %v", err)
			}
			if job["description"] != description {
				t.Errorf("description=%v, want full description", job["description"])
			}
		}
	}
}

func TestJobStats_NewJobsIsPendingAndCountsByState(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := t.Context()
	if _, err := db.InsertJobIfNew(ctx, pool, db.Job{
		JobSource: db.SourceLinkedIn,
		Title:     "IT Help Desk",
		Company:   "Acme",
		Location:  "Remote",
		JobURL:    "https://www.linkedin.com/jobs/view/api-stats-a/",
	}); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, err := db.InsertJobIfNew(ctx, pool, db.Job{
		JobSource: db.SourceLinkedIn,
		Title:     "IT Help Desk",
		Company:   "Acme",
		Location:  "Remote",
		JobURL:    "https://www.linkedin.com/jobs/view/api-stats-b/",
	}); err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET new = FALSE`); err != nil {
		t.Fatalf("clear legacy new flag: %v", err)
	}

	h := &Handler{DB: pool}
	req := httptest.NewRequest(http.MethodGet, "/api/v0/jobs/stats", nil)
	rec := httptest.NewRecorder()
	NewServer("", h).Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v0/jobs/stats status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("stats JSON: %v", err)
	}
	if body["new_jobs"] != float64(2) {
		t.Errorf("new_jobs = %v, want 2 (new_jobs is pending count, not the legacy new boolean)", body["new_jobs"])
	}
	if _, ok := body["by_state"].(map[string]any); !ok {
		t.Error("GET /api/v0/jobs/stats must report counts by state")
	}

	if _, err := pool.Exec(`UPDATE jobs SET state = 'rejected' WHERE job_url LIKE '%api-stats-b/'`); err != nil {
		t.Errorf("GET /jobs/stats must count by state: %v", err)
		return
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v0/jobs/stats", nil)
	rec = httptest.NewRecorder()
	NewServer("", h).Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v0/jobs/stats after reject status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("stats JSON: %v", err)
	}
	if body["new_jobs"] != float64(1) {
		t.Errorf("new_jobs after one reject = %v, want 1", body["new_jobs"])
	}
	byState, _ := body["by_state"].(map[string]any)
	if byState == nil {
		t.Fatal("GET /api/v0/jobs/stats must report counts by state")
	}
	if byState["pending"] != float64(1) {
		t.Errorf("by_state pending = %v, want 1", byState["pending"])
	}
	if byState["rejected"] != float64(1) {
		t.Errorf("by_state rejected = %v, want 1", byState["rejected"])
	}
	for _, st := range []string{"pending", "rejected", "ready", "applied", "dismissed"} {
		if _, ok := byState[st]; !ok {
			t.Errorf("by_state missing key %q", st)
		}
	}
}

func TestREADME_DocumentsNewJobsAsPending(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	readmePath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "README.md")
	body, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "new_jobs") || !strings.Contains(strings.ToLower(text), "pending") {
		t.Errorf("README must document that stats new_jobs is the pending count (path %s)", readmePath)
	}
}
