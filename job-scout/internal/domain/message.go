package domain

import (
	"fmt"
	"strings"
)

// BuildReadyMessage formats the ready-for-review webhook body.
func BuildReadyMessage(urls []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "there are %d new jobs ready for review:", len(urls))
	if len(urls) > 0 {
		b.WriteByte('\n')
		b.WriteString(strings.Join(urls, "\n"))
	}
	return b.String()
}
