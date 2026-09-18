package domain

import (
	"strings"

	"golang.org/x/text/cases"
)

var foldCaser = cases.Fold()

func fold(s string) string {
	return foldCaser.String(strings.TrimSpace(s))
}

func titleCompanyKey(job Job) string {
	return fold(job.Title) + "\x1e" + fold(job.Company)
}

func earlier(a, b Job) bool {
	if a.CreatedAt.Before(b.CreatedAt) {
		return true
	}
	if b.CreatedAt.Before(a.CreatedAt) {
		return false
	}
	return a.ID < b.ID
}

// DuplicateWinner returns the job to keep for one title+company group:
// earliest CreatedAt, then lower ID.
func DuplicateWinner(jobs []Job) Job {
	if len(jobs) == 0 {
		return Job{}
	}
	win := jobs[0]
	for _, j := range jobs[1:] {
		if earlier(j, win) {
			win = j
		}
	}
	return win
}

// IsDuplicateLoser reports whether job loses the title+company duplicate rule
// against the whole table.
func IsDuplicateLoser(job Job, all []Job) bool {
	key := titleCompanyKey(job)
	group := make([]Job, 0, 2)
	for _, other := range all {
		if titleCompanyKey(other) == key {
			group = append(group, other)
		}
	}
	if len(group) <= 1 {
		return false
	}
	return DuplicateWinner(group).ID != job.ID
}
