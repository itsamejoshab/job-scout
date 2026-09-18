package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Run(m))
}

func TestLoadNotifySnapshot_ReadsLiveListsAndWholeTableEachCall(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	if err := db.SeedSettings(ctx, pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.InsertJobIfNew(ctx, pool, db.Job{
		JobSource: db.SourceLinkedIn,
		Title:     "IT Help Desk",
		Company:   "Acme",
		Location:  "Remote",
		JobURL:    "https://www.linkedin.com/jobs/view/snap-pending/",
	}); err != nil {
		t.Fatalf("insert pending: %v", err)
	}
	if _, err := db.InsertJobIfNew(ctx, pool, db.Job{
		JobSource: db.SourceLinkedIn,
		Title:     "IT Help Desk",
		Company:   "OldCo",
		Location:  "Remote",
		JobURL:    "https://www.linkedin.com/jobs/view/snap-notified/",
	}); err != nil {
		t.Fatalf("insert notified: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET state = 'notified' WHERE job_url LIKE '%snap-notified/'`); err != nil {
		t.Fatalf("mark notified: %v", err)
	}

	raw, err := json.Marshal([]string{"only-from-db"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := pool.Exec(`UPDATE search_settings SET company_exclude = $1::json`, raw); err != nil {
		t.Fatalf("update lists: %v", err)
	}

	first, err := LoadNotifySnapshot(ctx, pool)
	if err != nil {
		t.Fatalf("LoadNotifySnapshot: %v", err)
	}
	if len(first.All) != 2 {
		t.Errorf("All = %d, want 2 (whole table)", len(first.All))
	}
	if len(first.Pending) != 1 {
		t.Errorf("Pending = %d, want 1", len(first.Pending))
	}
	if len(first.Lists.CompanyExclude) != 1 || first.Lists.CompanyExclude[0] != "only-from-db" {
		t.Errorf("first load company_exclude = %v, want [only-from-db] from DB", first.Lists.CompanyExclude)
	}

	raw2, err := json.Marshal([]string{"second-run"})
	if err != nil {
		t.Fatalf("marshal 2: %v", err)
	}
	if _, err := pool.Exec(`UPDATE search_settings SET company_exclude = $1::json`, raw2); err != nil {
		t.Fatalf("update lists 2: %v", err)
	}

	second, err := LoadNotifySnapshot(ctx, pool)
	if err != nil {
		t.Fatalf("LoadNotifySnapshot second run: %v", err)
	}
	if len(second.Lists.CompanyExclude) != 1 || second.Lists.CompanyExclude[0] != "second-run" {
		t.Errorf("second load company_exclude = %v, want [second-run] (Notify must reload lists each run)", second.Lists.CompanyExclude)
	}
}
