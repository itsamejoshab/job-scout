package domain

import (
	"strings"
	"time"
)

const (
	StatePending     = "pending"
	StateRejected    = "rejected"
	StateNeedsDetail = "needs_detail"
	StateReady       = "ready"

	ReasonDuplicate         = "duplicate"
	ReasonTitleCompany      = "title_company"
	ReasonDescription       = "description"
	ReasonDetailFailed      = "detail_failed"
	ReasonUnsupportedSource = "unsupported_source"

	MaxDetailAttempts = 3
)

// Job is one posting for notify filters. It is not a database row type.
type Job struct {
	ID             int64
	Title          string
	Company        string
	Description    string
	JobURL         string
	CreatedAt      time.Time
	DetailAttempts int
}

// Lists is the notify word lists loaded from search_settings.
type Lists struct {
	TitleInclude   []string
	TitleExclude   []string
	CompanyExclude []string
	DescInclude    []string
	DescExclude    []string
}

// Decision is the next persist for one pending job.
type Decision struct {
	State          string
	RejectReason   string
	NeedFetch      bool
	Description    string
	DetailAttempts int
}

// FilterPending applies steps 1–2 of the notify chain. If description is empty
// after those steps, NeedFetch is set so the caller can GET detail HTML.
func FilterPending(job Job, all []Job, lists Lists) Decision {
	base := Decision{DetailAttempts: job.DetailAttempts, Description: strings.TrimSpace(job.Description)}
	if IsDuplicateLoser(job, all) {
		base.State = StateRejected
		base.RejectReason = ReasonDuplicate
		base.Description = ""
		return base
	}
	if !passTitleCompany(job, lists) {
		base.State = StateRejected
		base.RejectReason = ReasonTitleCompany
		base.Description = ""
		return base
	}
	if strings.TrimSpace(job.Description) == "" {
		base.State = StateNeedsDetail
		base.NeedFetch = true
		base.Description = ""
		return base
	}
	return FilterAfterDescription(job, lists)
}

// FilterAfterDescription applies description include/exclude checks.
func FilterAfterDescription(job Job, lists Lists) Decision {
	if strings.TrimSpace(job.Description) == "" {
		return OnDetailFetchFailure(job)
	}
	d := Decision{Description: job.Description, DetailAttempts: job.DetailAttempts}
	if !passInclude(job.Description, lists.DescInclude) || !passExclude(job.Description, lists.DescExclude) {
		d.State = StateRejected
		d.RejectReason = ReasonDescription
		return d
	}
	d.State = StateReady
	return d
}

// OnDetailFetchFailure records one failed description GET for this notify run.
func OnDetailFetchFailure(job Job) Decision {
	attempts := job.DetailAttempts + 1
	if attempts >= MaxDetailAttempts {
		return Decision{State: StateRejected, RejectReason: ReasonDetailFailed, DetailAttempts: attempts}
	}
	return Decision{State: StateNeedsDetail, DetailAttempts: attempts}
}

func passTitleCompany(job Job, lists Lists) bool {
	if !passInclude(job.Title, lists.TitleInclude) {
		return false
	}
	if !passExclude(job.Title, lists.TitleExclude) {
		return false
	}
	return passExclude(job.Company, lists.CompanyExclude)
}

func passInclude(haystack string, needles []string) bool {
	if len(needles) == 0 {
		return true
	}
	return containsAny(haystack, needles)
}

func passExclude(haystack string, needles []string) bool {
	return !containsAny(haystack, needles)
}

func containsAny(haystack string, needles []string) bool {
	h := fold(haystack)
	for _, n := range needles {
		n = fold(n)
		if n == "" {
			continue
		}
		if strings.Contains(h, n) {
			return true
		}
	}
	return false
}
