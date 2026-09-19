package db

import (
	"context"
	"testing"
)

func TestListDuplicateCandidates_IncludesWholeTableAcrossStates(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()

	winner := sampleJob("https://www.linkedin.com/jobs/view/process-winner/")
	winner.Title = "IT Help Desk"
	winner.Company = "Acme"
	if _, err := InsertJobIfNew(ctx, pool, winner); err != nil {
		t.Fatalf("insert winner: %v", err)
	}
	loser := sampleJob("https://www.linkedin.com/jobs/view/process-loser/")
	loser.Title = winner.Title
	loser.Company = winner.Company
	if _, err := InsertJobIfNew(ctx, pool, loser); err != nil {
		t.Fatalf("insert loser: %v", err)
	}
	other := sampleJob("https://www.linkedin.com/jobs/view/process-other/")
	other.Title = "Desktop Support"
	if _, err := InsertJobIfNew(ctx, pool, other); err != nil {
		t.Fatalf("insert other: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET state = 'applied' WHERE job_url = $1`, winner.JobURL); err != nil {
		t.Fatalf("mark winner applied: %v", err)
	}

	group, err := ListDuplicateCandidates(ctx, pool)
	if err != nil {
		t.Fatalf("ListDuplicateCandidates: %v", err)
	}
	if len(group) != 3 {
		t.Fatalf("candidate len = %d, want 3", len(group))
	}
	if group[0].State != JobStateApplied {
		t.Errorf("winner state = %q, want applied row included", group[0].State)
	}
}
