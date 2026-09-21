package scraper

import (
	"testing"

	"github.com/jobscout/jobscout/internal/db"
)

func TestDeriveSearchIntention_LinkedIn(t *testing.T) {
	cases := []struct {
		name string
		fWT  string
		want string
	}{
		{"empty", "", db.SearchIntentionOnsite},
		{"all three empty via parse", "1,2,3", db.SearchIntentionOnsite},
		{"onsite only", "1", db.SearchIntentionOnsite},
		{"remote only", "2", db.SearchIntentionRemote},
		{"hybrid only", "3", db.SearchIntentionHybrid},
		{"remote and hybrid", "2,3", db.SearchIntentionRemoteHybrid},
		{"onsite and remote", "1,2", db.SearchIntentionOnsite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveSearchIntention(db.SourceLinkedIn, map[string]string{"f_WT": tc.fWT}, "")
			if got != tc.want {
				t.Errorf("f_WT=%q got %q want %q", tc.fWT, got, tc.want)
			}
		})
	}
}

func TestDeriveSearchIntention_IndeedFilter(t *testing.T) {
	cases := []struct {
		filter string
		want   string
	}{
		{"", db.SearchIntentionOnsite},
		{"remote", db.SearchIntentionRemote},
		{"hybrid", db.SearchIntentionHybrid},
		{"REMOTE", db.SearchIntentionRemote},
	}
	for _, tc := range cases {
		got := DeriveSearchIntention(db.SourceIndeed, map[string]string{
			"include_remote": "true", "include_hybrid": "true",
		}, tc.filter)
		if got != tc.want {
			t.Errorf("filter=%q got %q want %q", tc.filter, got, tc.want)
		}
	}
}

func TestDeriveSearchIntention_Fantastic(t *testing.T) {
	cases := []struct {
		name string
		work string
		want string
	}{
		{"empty", "", db.SearchIntentionOnsite},
		{"onsite", "On-site", db.SearchIntentionOnsite},
		{"remote solely", "Remote Solely", db.SearchIntentionRemote},
		{"remote ok", "Remote OK", db.SearchIntentionRemote},
		{"hybrid", "Hybrid", db.SearchIntentionHybrid},
		{"remote and hybrid", "Remote Solely,Hybrid", db.SearchIntentionRemoteHybrid},
		{"onsite wins", "On-site,Remote Solely", db.SearchIntentionOnsite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveSearchIntention(db.SourceFantastic, map[string]string{
				"aiWorkArrangementFilter": tc.work,
			}, "")
			if got != tc.want {
				t.Errorf("work=%q got %q want %q", tc.work, got, tc.want)
			}
		})
	}
}
