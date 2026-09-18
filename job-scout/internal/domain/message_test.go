package domain

import "testing"

func TestBuildMessage_MatchesSpecFormatIncludingZerosAndClaimedURLs(t *testing.T) {
	counts := MessageCounts{
		Total:        10,
		TitleCompany: 3,
		Description:  1,
		RemoteLie:    0,
		Duplicate:    2,
		DetailFailed: 1,
		Pending:      1,
		Eligible:     2,
		Notifying:    3,
		Notified:     0,
	}
	urls := []string{
		"https://www.linkedin.com/jobs/view/older/",
		"https://www.linkedin.com/jobs/view/newer/",
	}

	got := BuildMessage(counts, urls)
	want := "" +
		"  10 job postings stored.\n" +
		" -3 dont match companies or titles\n" +
		" -1 dont match descriptions\n" +
		" -0 are lying about remote\n" +
		" -2 duplicate title/company\n" +
		" -1 detail failed\n" +
		"  pending 1, eligible 2, notifying now 3, notified 0.\n" +
		"*************\n" +
		"NEW LEAD: Some new jobs were posted:\n" +
		"https://www.linkedin.com/jobs/view/older/\n" +
		"https://www.linkedin.com/jobs/view/newer/"
	if got != want {
		t.Errorf("BuildMessage mismatch\ngot:\n%q\nwant:\n%q", got, want)
	}
}

func TestBuildMessage_PrintsZeroRejectLinesAndClaimedURLsOnly(t *testing.T) {
	counts := MessageCounts{
		Total:     4,
		Pending:   0,
		Eligible:  1,
		Notifying: 2,
		Notified:  1,
	}
	got := BuildMessage(counts, []string{"https://www.linkedin.com/jobs/view/claimed-only/"})
	want := "" +
		"  4 job postings stored.\n" +
		" -0 dont match companies or titles\n" +
		" -0 dont match descriptions\n" +
		" -0 are lying about remote\n" +
		" -0 duplicate title/company\n" +
		" -0 detail failed\n" +
		"  pending 0, eligible 1, notifying now 2, notified 1.\n" +
		"*************\n" +
		"NEW LEAD: Some new jobs were posted:\n" +
		"https://www.linkedin.com/jobs/view/claimed-only/"
	if got != want {
		t.Errorf("BuildMessage mismatch\ngot:\n%q\nwant:\n%q", got, want)
	}
}
