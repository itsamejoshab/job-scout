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
		{
			Title: "IT Help Desk", Company: "Acme", Location: "Kansas", JobURL: "  " + url + "  ",
			Source: db.SourceLinkedIn, IsRemote: false,
			SearchContext: `query keywords="help desk" location="105493761" f_WT="3"`,
		},
	})
	if err != nil {
		t.Fatalf("save onsite: %v", err)
	}
	if saved != 1 {
		t.Errorf("saved = %d, want 1", saved)
	}

	saved, err = s.saveJobs(ctx, []JobData{
		{
			Title: "Other", Company: "Acme", Location: "Remote", JobURL: url,
			Source: db.SourceLinkedIn, IsRemote: true,
			SearchContext: `query keywords="help desk" location="103644278" f_WT="2"`,
		},
	})
	if err != nil {
		t.Fatalf("save remote resight: %v", err)
	}
	if saved != 0 {
		t.Errorf("duplicate scrape saved = %d, want 0", saved)
	}

	var (
		n             int
		remote        bool
		searchContext string
	)
	if err := pool.QueryRow(`SELECT COUNT(*), BOOL_OR(is_remote), MIN(search_context) FROM jobs WHERE job_url = $1`, url).
		Scan(&n, &remote, &searchContext); err != nil {
		t.Errorf("POST /api/v0/scrape persist path must apply is_remote OR: %v", err)
	} else {
		if n != 1 {
			t.Errorf("POST /api/v0/scrape persist path rows = %d, want 1", n)
		}
		if !remote {
			t.Error("POST /api/v0/scrape persist path must apply is_remote OR")
		}
		wantContext := `query keywords="help desk" location="105493761" f_WT="3"`
		if searchContext != wantContext {
			t.Errorf("search context = %q, want first source query %q", searchContext, wantContext)
		}
	}

	if _, err := pool.Exec(`UPDATE jobs SET state = 'applied', notified_at = now() WHERE job_url = $1`, url); err != nil {
		t.Errorf("debug scrape must use the jobs store state column: %v", err)
		return
	}
	if _, err := s.saveJobs(ctx, []JobData{
		{Title: "Other", Company: "Acme", Location: "Remote", JobURL: url, Source: db.SourceLinkedIn, IsRemote: false},
	}); err != nil {
		t.Fatalf("save after applied: %v", err)
	}
	var state string
	if err := pool.QueryRow(`SELECT state FROM jobs WHERE job_url = $1`, url).Scan(&state); err != nil {
		t.Fatalf("load state after scrape persist: %v", err)
	}
	if state != "applied" {
		t.Errorf("POST /api/v0/scrape persist path changed state to %q, want applied", state)
	}
}

func TestSaveJobs_DiceURLUniquenessAndRemoteOR(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewService(pool)
	url := "https://www.dice.com/job-detail/persist-dice"
	ctx := t.Context()

	saved, err := s.saveJobs(ctx, []JobData{{
		Title: "Desktop Support", Company: "Acme", Location: "Port Orange, FL", JobURL: url,
		Source: db.SourceDice, IsRemote: false,
		SearchContext: `query keywords="Desktop Support" location="Port Orange, FL" include_remote="true"`,
	}})
	if err != nil {
		t.Fatalf("save onsite: %v", err)
	}
	if saved != 1 {
		t.Errorf("saved = %d, want 1", saved)
	}

	saved, err = s.saveJobs(ctx, []JobData{{
		Title: "Other", Company: "Acme", Location: "Remote", JobURL: url,
		Source: db.SourceDice, IsRemote: true,
		SearchContext: `query keywords="other" location="Remote" include_remote="false"`,
	}})
	if err != nil {
		t.Fatalf("save remote resight: %v", err)
	}
	if saved != 0 {
		t.Errorf("duplicate Dice scrape saved = %d, want 0", saved)
	}

	var (
		n             int
		remote        bool
		searchContext string
		source        string
	)
	if err := pool.QueryRow(`SELECT COUNT(*), BOOL_OR(is_remote), MIN(search_context), MIN(job_source::text) FROM jobs WHERE job_url = $1`, url).
		Scan(&n, &remote, &searchContext, &source); err != nil {
		t.Fatalf("load Dice persist row: %v", err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
	if !remote {
		t.Error("Dice persist path must apply is_remote OR")
	}
	if source != "DICE" {
		t.Errorf("job_source = %q, want DICE", source)
	}
	wantContext := `query keywords="Desktop Support" location="Port Orange, FL" include_remote="true"`
	if searchContext != wantContext {
		t.Errorf("search_context = %q, want first source query %q", searchContext, wantContext)
	}
}
