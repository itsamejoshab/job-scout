package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Run(m))
}

func TestJobs_ListJSONIncludesState(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.InsertJobIfNew(t.Context(), pool, db.Job{
		JobSource: db.SourceLinkedIn,
		Title:     "IT Help Desk",
		Company:   "Acme",
		Location:  "Remote",
		JobURL:    "https://www.linkedin.com/jobs/view/api-list/",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	h := &Handler{DB: pool}
	req := httptest.NewRequest(http.MethodGet, "/api/v0/jobs", nil)
	rec := httptest.NewRecorder()
	NewServer("", h).Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v0/jobs status=%d body=%s", rec.Code, rec.Body.String())
	}
	var jobs []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &jobs); err != nil {
		t.Fatalf("jobs JSON: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs len=%d, want 1", len(jobs))
	}
	if jobs[0]["state"] != "pending" {
		t.Errorf("GET /api/v0/jobs state=%v, want pending", jobs[0]["state"])
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
	for _, st := range []string{"pending", "rejected", "eligible", "notifying", "notified"} {
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
