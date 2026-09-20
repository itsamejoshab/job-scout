package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
	"github.com/jobscout/jobscout/internal/domain"
	"github.com/jobscout/jobscout/internal/scraper"
)

func TestFilterPendingJob_WritesNeedsDetailWithoutGET(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(ctx, pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := pool.Exec(`
		UPDATE search_settings SET
			title_include = '["help desk"]'::json,
			title_exclude = '[]'::json,
			company_exclude = '[]'::json,
			desc_include_words = '["computer"]'::json,
			desc_exclude_words = '[]'::json
	`); err != nil {
		t.Fatalf("configure lists: %v", err)
	}

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ok, err := db.InsertJobIfNew(ctx, pool, db.Job{
		JobSource: db.SourceLinkedIn, Title: "IT Help Desk", Company: "Acme",
		Location: "Remote", JobURL: "https://www.linkedin.com/jobs/view/filter-needs/",
	})
	if err != nil || !ok {
		t.Fatalf("insert job: ok=%v err=%v", ok, err)
	}
	var jobID int64
	if err := pool.QueryRow(`SELECT id FROM jobs WHERE job_url LIKE '%filter-needs/'`).Scan(&jobID); err != nil {
		t.Fatalf("load id: %v", err)
	}

	acts := &Activities{DB: pool, Scraper: scraper.NewService(pool)}
	acts.Scraper.HTTPClient = server.Client()
	result, err := acts.filter_pending_job(ctx, jobID)
	if err != nil {
		t.Fatalf("filter_pending_job: %v", err)
	}
	if !result.WroteNeedsDetail {
		t.Error("empty description after cheap pass must write needs_detail")
	}
	if hits.Load() != 0 {
		t.Errorf("fast filter must not GET provider HTML, hits=%d", hits.Load())
	}
	row, err := db.GetJob(ctx, pool, jobID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.State != db.JobStateNeedsDetail {
		t.Errorf("state = %q, want needs_detail", row.State)
	}
}

func TestFilterPendingJob_UnsupportedSourceRejectsWithoutGET(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(ctx, pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := pool.Exec(`
		UPDATE search_settings SET
			title_include = '["help desk"]'::json,
			title_exclude = '[]'::json,
			company_exclude = '[]'::json,
			desc_include_words = '[]'::json,
			desc_exclude_words = '[]'::json
	`); err != nil {
		t.Fatalf("configure lists: %v", err)
	}

	ok, err := db.InsertJobIfNew(ctx, pool, db.Job{
		JobSource: db.SourceIndeed, Title: "IT Help Desk", Company: "Acme",
		Location: "Remote", JobURL: "https://www.indeed.com/viewjob?jk=filter-indeed",
	})
	if err != nil || !ok {
		t.Fatalf("insert job: ok=%v err=%v", ok, err)
	}
	var jobID int64
	if err := pool.QueryRow(`SELECT id FROM jobs WHERE job_url LIKE '%filter-indeed'`).Scan(&jobID); err != nil {
		t.Fatalf("load id: %v", err)
	}

	acts := &Activities{DB: pool, Scraper: scraper.NewService(pool)}
	result, err := acts.filter_pending_job(ctx, jobID)
	if err != nil {
		t.Fatalf("filter_pending_job: %v", err)
	}
	if result.WroteNeedsDetail {
		t.Error("unsupported source must not write needs_detail")
	}
	row, err := db.GetJob(ctx, pool, jobID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.State != db.JobStateRejected || row.RejectReason == nil || *row.RejectReason != domain.ReasonUnsupportedSource {
		t.Errorf("row state=%q reason=%v, want rejected unsupported_source", row.State, row.RejectReason)
	}
}

func TestFilterPendingJob_DiceEmptyDescriptionIsUnsupportedSource(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(ctx, pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := pool.Exec(`
		UPDATE search_settings SET
			title_include = '["help desk"]'::json,
			title_exclude = '[]'::json,
			company_exclude = '[]'::json,
			desc_include_words = '[]'::json,
			desc_exclude_words = '[]'::json
	`); err != nil {
		t.Fatalf("configure lists: %v", err)
	}

	ok, err := db.InsertJobIfNew(ctx, pool, db.Job{
		JobSource: db.SourceDice, Title: "IT Help Desk", Company: "Acme",
		Location: "Remote", JobURL: "https://www.dice.com/job-detail/filter-dice",
	})
	if err != nil || !ok {
		t.Fatalf("insert job: ok=%v err=%v", ok, err)
	}
	var jobID int64
	if err := pool.QueryRow(`SELECT id FROM jobs WHERE job_url LIKE '%filter-dice'`).Scan(&jobID); err != nil {
		t.Fatalf("load id: %v", err)
	}

	acts := &Activities{DB: pool, Scraper: scraper.NewService(pool)}
	result, err := acts.filter_pending_job(ctx, jobID)
	if err != nil {
		t.Fatalf("filter_pending_job: %v", err)
	}
	if result.WroteNeedsDetail {
		t.Error("Dice has no enricher; empty description must not write needs_detail")
	}
	row, err := db.GetJob(ctx, pool, jobID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.State != db.JobStateRejected || row.RejectReason == nil || *row.RejectReason != domain.ReasonUnsupportedSource {
		t.Errorf("Dice empty description state=%q reason=%v, want rejected unsupported_source", row.State, row.RejectReason)
	}
}
