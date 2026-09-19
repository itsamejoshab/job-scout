package domain

import "testing"

func TestBuildReadyMessage_ContainsOnlyCountAndClaimedURLs(t *testing.T) {
	urls := []string{
		"https://www.linkedin.com/jobs/view/older/",
		"https://www.linkedin.com/jobs/view/newer/",
	}

	got := BuildReadyMessage(urls)
	want := "" +
		"there are 2 new jobs ready for review:\n" +
		"https://www.linkedin.com/jobs/view/older/\n" +
		"https://www.linkedin.com/jobs/view/newer/"
	if got != want {
		t.Errorf("BuildReadyMessage mismatch\ngot:\n%q\nwant:\n%q", got, want)
	}
}

func TestBuildReadyMessage_UsesClaimedBatchLength(t *testing.T) {
	got := BuildReadyMessage([]string{"https://www.linkedin.com/jobs/view/claimed-only/"})
	want := "" +
		"there are 1 new jobs ready for review:\n" +
		"https://www.linkedin.com/jobs/view/claimed-only/"
	if got != want {
		t.Errorf("BuildReadyMessage mismatch\ngot:\n%q\nwant:\n%q", got, want)
	}
}
