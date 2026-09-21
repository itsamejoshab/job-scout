package scraper

import (
	"strings"
	"testing"

	"github.com/jobscout/jobscout/internal/db"
)

func TestQuerySearchContext_UsesStableLinkedInFields(t *testing.T) {
	got := querySearchContext(db.SourceLinkedIn, map[string]string{
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

func TestQuerySearchContext_DiceRecordsIncludeRemoteNotWorkType(t *testing.T) {
	got := querySearchContext(db.SourceDice, map[string]string{
		"keywords":       "Desktop Support",
		"location":       "Port Orange, FL",
		"include_remote": "true",
		"f_WT":           "2",
	})
	want := `query keywords="Desktop Support" location="Port Orange, FL" include_remote="true"`
	if got != want {
		t.Errorf("querySearchContext() = %q, want %q", got, want)
	}
	if strings.Contains(got, "f_WT") {
		t.Errorf("Dice search_context must not record f_WT, got %q", got)
	}
}

func TestQuerySearchContext_FantasticRecordsTitleLocationAndWork(t *testing.T) {
	got := querySearchContext(db.SourceFantastic, map[string]string{
		"keywords":                "Desktop Support, Application Support",
		"location":                "United States",
		"aiWorkArrangementFilter": "Remote Solely",
		"query_index":             "0",
	})
	want := `query titleSearch="Desktop Support, Application Support" locationSearch="United States" aiWorkArrangementFilter="Remote Solely"`
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
