package scraper

import "testing"

func TestQuerySearchContext_UsesStableLinkedInFields(t *testing.T) {
	got := querySearchContext(map[string]string{
		"keywords": "help desk",
		"location": "103644278",
		"f_WT":     "3,2",
		"ignored":  "not logged",
	})
	want := `query keywords="help desk" location="103644278" f_WT="3,2"`
	if got != want {
		t.Errorf("querySearchContext() = %q, want %q", got, want)
	}
}

func TestGlobalSearchContext_IdentifiesConfiguredSearch(t *testing.T) {
	got := globalSearchContext("Remote IT Help Desk near Port Orange FL")
	want := `global keywords="Remote IT Help Desk near Port Orange FL"`
	if got != want {
		t.Errorf("globalSearchContext() = %q, want %q", got, want)
	}
}
