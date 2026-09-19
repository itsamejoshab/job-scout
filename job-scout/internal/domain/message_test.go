package domain

import "testing"

func TestBuildMessage_MatchesSpecFormatIncludingZerosAndClaimedURLs(t *testing.T) {
	counts := MessageCounts{
		Total:        10,
		TitleCompany: 3,
		Description:  1,
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
		"Job alert summary:\n" +
		"  10 job postings scraped\n" +
		" -3 dont match companies or titles\n" +
		" -1 dont match descriptions\n" +
		" -2 duplicate title/company\n" +
		" -1 lost due to unforseen circumstances\n" +
		"\n" +
		"2 new jobs to check out\n" +
		"*************\n" +
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
		"Job alert summary:\n" +
		"  4 job postings scraped\n" +
		" -0 dont match companies or titles\n" +
		" -0 dont match descriptions\n" +
		" -0 duplicate title/company\n" +
		" -0 lost due to unforseen circumstances\n" +
		"\n" +
		"1 new jobs to check out\n" +
		"*************\n" +
		"https://www.linkedin.com/jobs/view/claimed-only/"
	if got != want {
		t.Errorf("BuildMessage mismatch\ngot:\n%q\nwant:\n%q", got, want)
	}
}
