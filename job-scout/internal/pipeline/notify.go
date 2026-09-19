package pipeline

import (
	"context"
	"database/sql"
	"time"

	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/domain"
)

const (
	// ScheduledNotifyWorkflowID is the reserved ID for the notify schedule.
	ScheduledNotifyWorkflowID = "jobscout-notify-scheduled"
	manualNotifyIDPrefix      = "jobscout-notify-manual-"
)

// ManualNotifyWorkflowID returns a unique operator notify ID that cannot collide
// with the reserved scheduled ID.
func ManualNotifyWorkflowID(now time.Time) string {
	return manualNotifyIDPrefix + now.UTC().Format(time.RFC3339Nano)
}

// NotifySnapshot is the DB view NotifyWorkflow needs to filter pending jobs.
type NotifySnapshot struct {
	Pending []domain.Job
	All     []domain.Job
	Lists   domain.Lists
}

// ApplyJobDecisionInput persists one filter decision.
type ApplyJobDecisionInput struct {
	JobID    int64
	Decision domain.Decision
}

// LoadNotifySnapshot reads live search_settings and every job row.
func LoadNotifySnapshot(ctx context.Context, database *sql.DB) (NotifySnapshot, error) {
	settings, err := db.GetSearchSettings(ctx, database)
	if err != nil {
		return NotifySnapshot{}, err
	}
	lists := domain.Lists{}
	if settings != nil {
		lists = domain.Lists{
			TitleInclude:   settings.TitleInclude,
			TitleExclude:   settings.TitleExclude,
			CompanyExclude: settings.CompanyExclude,
			DescInclude:    settings.DescIncludeWords,
			DescExclude:    settings.DescExcludeWords,
		}
	}

	rows, err := db.ListJobsForNotify(ctx, database)
	if err != nil {
		return NotifySnapshot{}, err
	}

	all := make([]domain.Job, 0, len(rows))
	pending := make([]domain.Job, 0)
	for _, j := range rows {
		dj := domainJobFromDB(j)
		all = append(all, dj)
		if j.State == db.JobStatePending {
			pending = append(pending, dj)
		}
	}
	return NotifySnapshot{Pending: pending, All: all, Lists: lists}, nil
}

func domainJobFromDB(j db.Job) domain.Job {
	desc := ""
	if j.Description != nil {
		desc = *j.Description
	}
	return domain.Job{
		ID:             j.ID,
		Title:          j.Title,
		Company:        j.Company,
		Description:    desc,
		JobURL:         j.JobURL,
		CreatedAt:      j.CreatedAt,
		DetailAttempts: j.DetailAttempts,
	}
}
