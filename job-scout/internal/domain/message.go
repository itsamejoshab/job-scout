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
	fmt.Fprintf(&b, "  %d job postings stored.\n", counts.Total)
	fmt.Fprintf(&b, " -%d dont match companies or titles\n", counts.TitleCompany)
	fmt.Fprintf(&b, " -%d dont match descriptions\n", counts.Description)
	fmt.Fprintf(&b, " -%d are lying about remote\n", counts.RemoteLie)
	fmt.Fprintf(&b, " -%d duplicate title/company\n", counts.Duplicate)
	fmt.Fprintf(&b, " -%d detail failed\n", counts.DetailFailed)
	fmt.Fprintf(&b, "  pending %d, eligible %d, notifying now %d, notified %d.\n",
		counts.Pending, counts.Eligible, counts.Notifying, counts.Notified)
	b.WriteString("*************\n")
	b.WriteString("NEW LEAD: Some new jobs were posted:")
	if len(urls) > 0 {
		b.WriteByte('\n')
		b.WriteString(strings.Join(urls, "\n"))
	}
	return b.String()
}
