package domain

import (
	"testing"
	"time"
)

func TestDuplicateWinner_EarliestCreatedAtThenLowerID(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	jobs := []Job{
		{ID: 4, Title: "IT Help Desk", Company: "Acme", CreatedAt: now.Add(2 * time.Minute)},
		{ID: 9, Title: "IT Help Desk", Company: "Acme", CreatedAt: now},
		{ID: 2, Title: "IT Help Desk", Company: "Acme", CreatedAt: now},
		{ID: 3, Title: "Other Role", Company: "Acme", CreatedAt: now.Add(-time.Hour)},
	}

	got := DuplicateWinner(jobs[:3])
	if got.ID != 2 {
		t.Errorf("DuplicateWinner ID = %d, want 2 (earliest created_at, then lower id)", got.ID)
	}

	sameTime := []Job{
		{ID: 20, Title: "Support", Company: "Globex", CreatedAt: now},
		{ID: 8, Title: "Support", Company: "Globex", CreatedAt: now},
	}
	got = DuplicateWinner(sameTime)
	if got.ID != 8 {
		t.Errorf("DuplicateWinner ID = %d, want 8 (lower id when created_at is equal)", got.ID)
	}
}

func TestFilterPending_DuplicateDoesNotDropWinner(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	lists := Lists{TitleInclude: []string{"Help Desk"}, DescInclude: []string{"computer"}}
	winner := Job{
		ID:          2,
		Title:       "IT Help Desk",
		Company:     "Acme",
		Description: "computer windows",
		CreatedAt:   now,
	}
	loser := Job{
		ID:        4,
		Title:     "IT Help Desk",
		Company:   "Acme",
		CreatedAt: now.Add(time.Minute),
	}
	all := []Job{winner, loser}

	got := FilterPending(winner, all, lists)
	if got.State != StateEligible {
		t.Errorf("winner State = %q, want eligible (row kept, not rejected)", got.State)
	}
	if got.RejectReason != "" {
		t.Errorf("winner RejectReason = %q, want empty", got.RejectReason)
	}

	gotLoser := FilterPending(loser, all, lists)
	if gotLoser.State != StateRejected || gotLoser.RejectReason != ReasonDuplicate {
		t.Errorf("loser = %+v, want rejected/duplicate (row kept, not deleted)", gotLoser)
	}
}
