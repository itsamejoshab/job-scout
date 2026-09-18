package scraper

import (
	"os"
	"testing"

	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Run(m))
}

func TestSaveJobs_UsesSameUniquenessAndRemoteOR(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewService(pool)
	url := "https://www.linkedin.com/jobs/view/scrape-path/"
	ctx := t.Context()

	saved, err := s.saveJobs(ctx, []JobData{
		{Title: "IT Help Desk", Company: "Acme", Location: "Remote", JobURL: "  " + url + "  ", Source: db.SourceLinkedIn, IsRemote: false},
	})
	if err != nil {
		t.Fatalf("save onsite: %v", err)
	}
	if saved != 1 {
		t.Errorf("saved = %d, want 1", saved)
	}

	saved, err = s.saveJobs(ctx, []JobData{
		{Title: "Other", Company: "Acme", Location: "Remote", JobURL: url, Source: db.SourceLinkedIn, IsRemote: true},
	})
	if err != nil {
		t.Fatalf("save remote resight: %v", err)
	}
	if saved != 0 {
		t.Errorf("duplicate scrape saved = %d, want 0", saved)
	}

	var (
		n      int
		remote bool
	)
	if err := pool.QueryRow(`SELECT COUNT(*), BOOL_OR(is_remote) FROM jobs WHERE job_url = $1`, url).
		Scan(&n, &remote); err != nil {
		t.Errorf("POST /api/v0/scrape persist path must apply is_remote OR: %v", err)
	} else {
		if n != 1 {
			t.Errorf("POST /api/v0/scrape persist path rows = %d, want 1", n)
		}
		if !remote {
			t.Error("POST /api/v0/scrape persist path must apply is_remote OR")
		}
	}

	if _, err := pool.Exec(`UPDATE jobs SET state = 'notified' WHERE job_url = $1`, url); err != nil {
		t.Errorf("debug scrape must use the jobs store state column: %v", err)
		return
	}
	if _, err := s.saveJobs(ctx, []JobData{
		{Title: "Other", Company: "Acme", Location: "Remote", JobURL: url, Source: db.SourceLinkedIn, IsRemote: false},
	}); err != nil {
		t.Fatalf("save after notified: %v", err)
	}
	var state string
	if err := pool.QueryRow(`SELECT state FROM jobs WHERE job_url = $1`, url).Scan(&state); err != nil {
		t.Fatalf("load state after scrape persist: %v", err)
	}
	if state != "notified" {
		t.Errorf("POST /api/v0/scrape persist path changed state to %q, want notified", state)
	}
}
