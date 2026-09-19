package scraper

import "testing"

func TestQuerySearchContext_UsesStableLinkedInFields(t *testing.T) {
	got := querySearchContext(map[string]string{
		"keywords": "help desk",
		"location": "103644278",
		"f_WT":     "3",
		"ignored":  "not logged",
	})
	want := `query keywords="help desk" location="103644278" f_WT="3"`
	if got != want {
		t.Errorf("querySearchContext() = %q, want %q", got, want)
	}
}

func TestHardcodedSearchContext_IdentifiesConfiguredSearch(t *testing.T) {
	got := hardcodedSearchContext(map[string]any{
		"description": "US remote",
		"is_remote":   true,
		"url":         "https://www.linkedin.com/jobs/search/?f_WT=2",
	})
	want := `hardcoded description="US remote" is_remote=true url="https://www.linkedin.com/jobs/search/?f_WT=2"`
	if got != want {
		t.Errorf("hardcodedSearchContext() = %q, want %q", got, want)
	}
}
