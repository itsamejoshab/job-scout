package domain

import (
	"fmt"
	"strings"
)

// MessageCounts is whole-table inventory after a notify claim and before POST.
type MessageCounts struct {
	Total        int
	TitleCompany int
	Description  int
	RemoteLie    int
	Duplicate    int
	DetailFailed int
	Pending      int
	Eligible     int
	Notifying    int
	Notified     int
}

// BuildMessage formats the Home Assistant lead body. Counts are funnel inventory;
// urls are this claimed batch only, oldest first.
func BuildMessage(counts MessageCounts, urls []string) string {
	var b strings.Builder
	b.WriteString("Job alert summary:\n")
	fmt.Fprintf(&b, "  %d job postings scraped\n", counts.Total)
	fmt.Fprintf(&b, " -%d dont match companies or titles\n", counts.TitleCompany)
	fmt.Fprintf(&b, " -%d dont match descriptions\n", counts.Description)
	fmt.Fprintf(&b, " -%d are lying about remote\n", counts.RemoteLie)
	fmt.Fprintf(&b, " -%d duplicate title/company\n", counts.Duplicate)
	fmt.Fprintf(&b, " -%d lost due to unforseen circumstances\n", counts.DetailFailed)
	fmt.Fprintf(&b, "\n%d new jobs to check out\n", len(urls))
	b.WriteString("*************")
	if len(urls) > 0 {
		b.WriteByte('\n')
		b.WriteString(strings.Join(urls, "\n"))
	}
	return b.String()
}
