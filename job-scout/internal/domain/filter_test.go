package domain

import (
	"testing"
	"time"
)

func TestFilterAfterDescription_EmptyBodyIsFetchFailure(t *testing.T) {
	job := Job{ID: 42, Description: "", DetailAttempts: 1}

	got := FilterAfterDescription(job, Lists{})

	if got.State != StateNeedsDetail || got.DetailAttempts != 2 {
		t.Errorf("empty body decision = %+v, want needs_detail with detail_attempts=2", got)
	}
}

func TestFilterPending_Table(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	lists := Lists{
		TitleInclude:   []string{"Help Desk", "Support"},
		TitleExclude:   []string{"manager", "intern"},
		CompanyExclude: []string{"ClickJobs.io", "Brooksource"},
		DescInclude:    []string{"computer", "windows"},
		DescExclude:    []string{"security clearance"},
	}

	helpDesk := Job{
		ID:        10,
		Title:     "IT Help Desk",
		Company:   "Acme",
		JobURL:    "https://www.linkedin.com/jobs/view/10/",
		CreatedAt: now,
	}

	tests := []struct {
		name string
		job  Job
		all  []Job
		list Lists
		want Decision
	}{
		{
			name: "duplicate loser later created_at rejected duplicate",
			job: Job{
				ID:        2,
				Title:     "IT Help Desk",
				Company:   "Acme",
				CreatedAt: now.Add(time.Minute),
				JobURL:    "https://www.linkedin.com/jobs/view/2/",
			},
			all: []Job{
				{ID: 1, Title: "IT Help Desk", Company: "Acme", CreatedAt: now, JobURL: "https://www.linkedin.com/jobs/view/1/"},
				{ID: 2, Title: "IT Help Desk", Company: "Acme", CreatedAt: now.Add(time.Minute), JobURL: "https://www.linkedin.com/jobs/view/2/"},
			},
			list: lists,
			want: Decision{State: StateRejected, RejectReason: ReasonDuplicate},
		},
		{
			name: "duplicate trim and unicode case-fold",
			job: Job{
				ID:        4,
				Title:     "  CAFÉ Support  ",
				Company:   "ACME",
				CreatedAt: now.Add(time.Second),
			},
			all: []Job{
				{ID: 3, Title: "café support", Company: "acme", CreatedAt: now},
				{ID: 4, Title: "  CAFÉ Support  ", Company: "ACME", CreatedAt: now.Add(time.Second)},
			},
			list: lists,
			want: Decision{State: StateRejected, RejectReason: ReasonDuplicate},
		},
		{
			name: "duplicate german eszett case-fold",
			job: Job{
				ID:        6,
				Title:     "STRASSE Support",
				Company:   "Acme",
				CreatedAt: now.Add(time.Second),
			},
			all: []Job{
				{ID: 5, Title: "straße Support", Company: "Acme", CreatedAt: now},
				{ID: 6, Title: "STRASSE Support", Company: "Acme", CreatedAt: now.Add(time.Second)},
			},
			list: lists,
			want: Decision{State: StateRejected, RejectReason: ReasonDuplicate},
		},
		{
			name: "duplicate lower id wins when created_at equal",
			job: Job{
				ID:        20,
				Title:     "IT Help Desk",
				Company:   "Acme",
				CreatedAt: now,
			},
			all: []Job{
				{ID: 20, Title: "IT Help Desk", Company: "Acme", CreatedAt: now},
				{ID: 8, Title: "IT Help Desk", Company: "Acme", CreatedAt: now},
			},
			list: lists,
			want: Decision{State: StateRejected, RejectReason: ReasonDuplicate},
		},
		{
			name: "duplicate vs whole table including notified",
			job: Job{
				ID:        12,
				Title:     "IT Help Desk",
				Company:   "Acme",
				CreatedAt: now.Add(time.Hour),
			},
			all: []Job{
				{ID: 11, Title: "IT Help Desk", Company: "Acme", CreatedAt: now},
				{ID: 12, Title: "IT Help Desk", Company: "Acme", CreatedAt: now.Add(time.Hour)},
			},
			list: lists,
			want: Decision{State: StateRejected, RejectReason: ReasonDuplicate},
		},
		{
			name: "title include any substring case-insensitive",
			job: Job{
				ID:          30,
				Title:       "senior help desk analyst",
				Company:     "Acme",
				Description: "computer troubleshooting",
				CreatedAt:   now,
			},
			all:  []Job{{ID: 30, Title: "senior help desk analyst", Company: "Acme", Description: "computer troubleshooting", CreatedAt: now}},
			list: lists,
			want: Decision{State: StateReady, Description: "computer troubleshooting"},
		},
		{
			name: "empty title include is no include constraint",
			job: Job{
				ID:          31,
				Title:       "Warehouse Clerk",
				Company:     "Acme",
				Description: "computer work on windows",
				CreatedAt:   now,
			},
			all: []Job{{ID: 31, Title: "Warehouse Clerk", Company: "Acme", Description: "computer work on windows", CreatedAt: now}},
			list: Lists{
				TitleExclude:   lists.TitleExclude,
				CompanyExclude: lists.CompanyExclude,
				DescInclude:    lists.DescInclude,
				DescExclude:    lists.DescExclude,
			},
			want: Decision{State: StateReady, Description: "computer work on windows"},
		},
		{
			name: "title exclude rejects",
			job: Job{
				ID:        32,
				Title:     "IT Help Desk Manager",
				Company:   "Acme",
				CreatedAt: now,
			},
			all:  []Job{{ID: 32, Title: "IT Help Desk Manager", Company: "Acme", CreatedAt: now}},
			list: lists,
			want: Decision{State: StateRejected, RejectReason: ReasonTitleCompany},
		},
		{
			name: "company exclude rejects matches",
			job: Job{
				ID:        33,
				Title:     "IT Help Desk",
				Company:   "ClickJobs.io Staffing",
				CreatedAt: now,
			},
			all:  []Job{{ID: 33, Title: "IT Help Desk", Company: "ClickJobs.io Staffing", CreatedAt: now}},
			list: lists,
			want: Decision{State: StateRejected, RejectReason: ReasonTitleCompany},
		},
		{
			name: "empty exclude lists mean no excludes",
			job: Job{
				ID:          34,
				Title:       "IT Help Desk Manager",
				Company:     "ClickJobs.io",
				Description: "computer windows",
				CreatedAt:   now,
			},
			all: []Job{{ID: 34, Title: "IT Help Desk Manager", Company: "ClickJobs.io", Description: "computer windows", CreatedAt: now}},
			list: Lists{
				TitleInclude: lists.TitleInclude,
				DescInclude:  lists.DescInclude,
			},
			want: Decision{State: StateReady, Description: "computer windows"},
		},
		{
			name: "title include miss is title_company",
			job: Job{
				ID:        35,
				Title:     "Registered Nurse",
				Company:   "Acme",
				CreatedAt: now,
			},
			all:  []Job{{ID: 35, Title: "Registered Nurse", Company: "Acme", CreatedAt: now}},
			list: lists,
			want: Decision{State: StateRejected, RejectReason: ReasonTitleCompany},
		},
		{
			name: "missing description needs fetch after title pass",
			job:  helpDesk,
			all:  []Job{helpDesk},
			list: lists,
			want: Decision{State: StateNeedsDetail, NeedFetch: true, DetailAttempts: 0},
		},
		{
			name: "whitespace description needs fetch",
			job: Job{
				ID:          36,
				Title:       "IT Help Desk",
				Company:     "Acme",
				Description: "   \n",
				CreatedAt:   now,
			},
			all:  []Job{{ID: 36, Title: "IT Help Desk", Company: "Acme", Description: "   \n", CreatedAt: now}},
			list: lists,
			want: Decision{State: StateNeedsDetail, NeedFetch: true},
		},
		{
			name: "empty parsed description still filtered",
			job: Job{
				ID:          37,
				Title:       "IT Help Desk",
				Company:     "Acme",
				Description: "",
				CreatedAt:   now,
			},
			all:  []Job{{ID: 37, Title: "IT Help Desk", Company: "Acme", CreatedAt: now}},
			list: lists,
			want: Decision{State: StateNeedsDetail, NeedFetch: true},
		},
		{
			name: "description exclude rejects",
			job: Job{
				ID:          38,
				Title:       "IT Help Desk",
				Company:     "Acme",
				Description: "computer windows with security clearance required",
				CreatedAt:   now,
			},
			all:  []Job{{ID: 38, Title: "IT Help Desk", Company: "Acme", Description: "computer windows with security clearance required", CreatedAt: now}},
			list: lists,
			want: Decision{State: StateRejected, RejectReason: ReasonDescription, Description: "computer windows with security clearance required"},
		},
		{
			name: "description include miss rejects",
			job: Job{
				ID:          39,
				Title:       "IT Help Desk",
				Company:     "Acme",
				Description: "serve food and greet guests",
				CreatedAt:   now,
			},
			all:  []Job{{ID: 39, Title: "IT Help Desk", Company: "Acme", Description: "serve food and greet guests", CreatedAt: now}},
			list: lists,
			want: Decision{State: StateRejected, RejectReason: ReasonDescription, Description: "serve food and greet guests"},
		},
		{
			name: "empty description include is no include constraint",
			job: Job{
				ID:          40,
				Title:       "IT Help Desk",
				Company:     "Acme",
				Description: "serve food",
				CreatedAt:   now,
			},
			all: []Job{{ID: 40, Title: "IT Help Desk", Company: "Acme", Description: "serve food", CreatedAt: now}},
			list: Lists{
				TitleInclude:   lists.TitleInclude,
				TitleExclude:   lists.TitleExclude,
				CompanyExclude: lists.CompanyExclude,
				DescExclude:    lists.DescExclude,
			},
			want: Decision{State: StateReady, Description: "serve food"},
		},
		{
			name: "description include any one substring is enough",
			job: Job{
				ID:          44,
				Title:       "IT Help Desk",
				Company:     "Acme",
				Description: "we use computer hardware daily",
				CreatedAt:   now,
			},
			all:  []Job{{ID: 44, Title: "IT Help Desk", Company: "Acme", Description: "we use computer hardware daily", CreatedAt: now}},
			list: lists,
			want: Decision{State: StateReady, Description: "we use computer hardware daily"},
		},
		{
			name: "same title different company is not duplicate",
			job: Job{
				ID:          45,
				Title:       "IT Help Desk",
				Company:     "Globex",
				Description: "computer troubleshooting",
				CreatedAt:   now.Add(time.Minute),
			},
			all: []Job{
				{ID: 1, Title: "IT Help Desk", Company: "Acme", Description: "computer troubleshooting", CreatedAt: now},
				{ID: 45, Title: "IT Help Desk", Company: "Globex", Description: "computer troubleshooting", CreatedAt: now.Add(time.Minute)},
			},
			list: lists,
			want: Decision{State: StateReady, Description: "computer troubleshooting"},
		},
		{
			name: "same company different title is not duplicate",
			job: Job{
				ID:          46,
				Title:       "Application Support",
				Company:     "Acme",
				Description: "computer troubleshooting",
				CreatedAt:   now.Add(time.Minute),
			},
			all: []Job{
				{ID: 1, Title: "IT Help Desk", Company: "Acme", Description: "computer troubleshooting", CreatedAt: now},
				{ID: 46, Title: "Application Support", Company: "Acme", Description: "computer troubleshooting", CreatedAt: now.Add(time.Minute)},
			},
			list: lists,
			want: Decision{State: StateReady, Description: "computer troubleshooting"},
		},
		{
			name: "passers are ready for review",
			job: Job{
				ID:          43,
				Title:       "Application Support",
				Company:     "Globex",
				Description: "computer desktop support",
				CreatedAt:   now,
			},
			all:  []Job{{ID: 43, Title: "Application Support", Company: "Globex", Description: "computer desktop support", CreatedAt: now}},
			list: lists,
			want: Decision{State: StateReady, Description: "computer desktop support"},
		},
		{
			name: "first failure stops at duplicate not title_company",
			job: Job{
				ID:        51,
				Title:     "IT Help Desk Manager",
				Company:   "ClickJobs.io",
				CreatedAt: now.Add(time.Minute),
			},
			all: []Job{
				{ID: 50, Title: "IT Help Desk Manager", Company: "ClickJobs.io", CreatedAt: now},
				{ID: 51, Title: "IT Help Desk Manager", Company: "ClickJobs.io", CreatedAt: now.Add(time.Minute)},
			},
			list: lists,
			want: Decision{State: StateRejected, RejectReason: ReasonDuplicate},
		},
		{
			name: "first failure stops at title_company not description",
			job: Job{
				ID:          52,
				Title:       "Registered Nurse",
				Company:     "Acme",
				Description: "serve food",
				CreatedAt:   now,
			},
			all:  []Job{{ID: 52, Title: "Registered Nurse", Company: "Acme", Description: "serve food", CreatedAt: now}},
			list: lists,
			want: Decision{State: StateRejected, RejectReason: ReasonTitleCompany},
		},
		{
			name: "description failure rejects before ready",
			job: Job{
				ID:          53,
				Title:       "IT Help Desk",
				Company:     "Acme",
				Description: "security clearance and travel to office",
				CreatedAt:   now,
			},
			all:  []Job{{ID: 53, Title: "IT Help Desk", Company: "Acme", Description: "security clearance and travel to office", CreatedAt: now}},
			list: lists,
			want: Decision{State: StateRejected, RejectReason: ReasonDescription, Description: "security clearance and travel to office"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterPending(tt.job, tt.all, tt.list)
			assertDecision(t, got, tt.want)
		})
	}
}

func TestFilterAfterDescription_EmptyParsedTextIsThirdFetchFailure(t *testing.T) {
	lists := Lists{
		TitleInclude: []string{"Help Desk"},
		DescInclude:  []string{"computer"},
	}
	job := Job{ID: 1, Title: "IT Help Desk", Company: "Acme", Description: "", DetailAttempts: 2}
	got := FilterAfterDescription(job, lists)
	assertDecision(t, got, Decision{State: StateRejected, RejectReason: ReasonDetailFailed, DetailAttempts: 3})
}

func TestFilterAfterDescription_RemoteLie(t *testing.T) {
	lists := Lists{
		DescInclude:    []string{"computer"},
		OnsiteKeywords: []string{"onsite", "in office"},
		RemoteKeywords: []string{"remote", "work from home"},
		HybridKeywords: []string{"hybrid"},
	}
	base := Job{
		ID: 1, Title: "IT Help Desk", Company: "Acme", Location: "Port Orange, FL",
		Description: "Support computers remotely from home.",
	}

	t.Run("onsite intention skips check", func(t *testing.T) {
		job := base
		job.SearchIntention = IntentionOnsite
		job.Description = "Must be onsite. Support computers."
		got := FilterAfterDescription(job, lists)
		assertDecision(t, got, Decision{State: StateReady, Description: job.Description})
	})

	t.Run("remote rejects onsite keyword", func(t *testing.T) {
		job := base
		job.SearchIntention = IntentionRemote
		job.Description = "Remote role but required to be onsite. Support computers."
		got := FilterAfterDescription(job, lists)
		assertDecision(t, got, Decision{
			State: StateRejected, RejectReason: ReasonRemoteLie, Description: job.Description,
		})
	})

	t.Run("remote requires remote keyword", func(t *testing.T) {
		job := base
		job.SearchIntention = IntentionRemote
		job.Description = "Support computers in your area."
		got := FilterAfterDescription(job, lists)
		assertDecision(t, got, Decision{
			State: StateRejected, RejectReason: ReasonRemoteLie, Description: job.Description,
		})
	})

	t.Run("remote passes with remote keyword", func(t *testing.T) {
		job := base
		job.SearchIntention = IntentionRemote
		job.Description = "Fully remote. Support computers."
		got := FilterAfterDescription(job, lists)
		assertDecision(t, got, Decision{State: StateReady, Description: job.Description})
	})

	t.Run("remote_hybrid accepts hybrid or remote", func(t *testing.T) {
		job := base
		job.SearchIntention = IntentionRemoteHybrid
		job.Description = "Hybrid schedule. Support computers."
		got := FilterAfterDescription(job, lists)
		assertDecision(t, got, Decision{State: StateReady, Description: job.Description})
	})

	t.Run("remote_lie before description exclude", func(t *testing.T) {
		job := base
		job.SearchIntention = IntentionRemote
		job.Description = "Support computers. security clearance required."
		got := FilterAfterDescription(job, Lists{
			DescInclude:    []string{"computer"},
			DescExclude:    []string{"security clearance"},
			RemoteKeywords: []string{"remote"},
		})
		assertDecision(t, got, Decision{
			State: StateRejected, RejectReason: ReasonRemoteLie, Description: job.Description,
		})
	})
}

func TestOnDetailFetchFailure_ThreeFailuresReject(t *testing.T) {
	job := Job{ID: 7, Title: "IT Help Desk", Company: "Acme", DetailAttempts: 0}
	first := OnDetailFetchFailure(job)
	assertDecision(t, first, Decision{State: StateNeedsDetail, DetailAttempts: 1})

	job.DetailAttempts = 1
	second := OnDetailFetchFailure(job)
	assertDecision(t, second, Decision{State: StateNeedsDetail, DetailAttempts: 2})

	job.DetailAttempts = 2
	third := OnDetailFetchFailure(job)
	assertDecision(t, third, Decision{State: StateRejected, RejectReason: ReasonDetailFailed, DetailAttempts: 3})
}

func assertDecision(t *testing.T, got, want Decision) {
	t.Helper()
	if got != want {
		t.Errorf("Decision = %+v, want %+v", got, want)
	}
}
