package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/scraper"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

const (
	ScrapeScheduleID = "jobscout-scrape"
	NotifyScheduleID = "jobscout-notify"
)

// ScheduleStore is the Temporal schedule client surface API boot uses.
type ScheduleStore interface {
	Create(ctx context.Context, options client.ScheduleOptions) (client.ScheduleHandle, error)
	GetHandle(ctx context.Context, scheduleID string) client.ScheduleHandle
}

// EnsureSchedules creates or updates the scrape and notify schedules.
func EnsureSchedules(ctx context.Context, schedules ScheduleStore, cfg config.Config) error {
	if schedules == nil {
		return fmt.Errorf("temporal schedule client is required")
	}
	for _, opts := range []client.ScheduleOptions{scrapeSchedule(cfg), notifySchedule(cfg)} {
		if err := upsertSchedule(ctx, schedules, opts); err != nil {
			return err
		}
	}
	return nil
}

func upsertSchedule(ctx context.Context, schedules ScheduleStore, opts client.ScheduleOptions) error {
	_, err := schedules.Create(ctx, opts)
	if err == nil {
		return nil
	}
	if !errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
		return err
	}
	return schedules.GetHandle(ctx, opts.ID).Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: func(in client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			sched := in.Description.Schedule
			spec := opts.Spec
			sched.Action = opts.Action
			sched.Spec = &spec
			if sched.Policy == nil {
				sched.Policy = &client.SchedulePolicies{}
			}
			sched.Policy.Overlap = opts.Overlap
			return &client.ScheduleUpdate{Schedule: &sched}, nil
		},
	})
}

func scrapeSchedule(cfg config.Config) client.ScheduleOptions {
	return client.ScheduleOptions{
		ID: ScrapeScheduleID,
		Spec: client.ScheduleSpec{
			Intervals:    []client.ScheduleIntervalSpec{{Every: time.Duration(cfg.ScrapeScheduleSeconds) * time.Second}},
			TimeZoneName: scheduleTimeZone(cfg),
		},
		Action: &client.ScheduleWorkflowAction{
			ID:        ScheduledScrapeWorkflowID,
			Workflow:  ScrapeWorkflow,
			Args:      []interface{}{scraper.TickInput{}},
			TaskQueue: config.TaskQueue,
		},
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	}
}

func notifySchedule(cfg config.Config) client.ScheduleOptions {
	return client.ScheduleOptions{
		ID: NotifyScheduleID,
		Spec: client.ScheduleSpec{
			CronExpressions: []string{notifyCron(cfg)},
			TimeZoneName:    scheduleTimeZone(cfg),
		},
		Action: &client.ScheduleWorkflowAction{
			ID:        ScheduledNotifyWorkflowID,
			Workflow:  NotifyWorkflow,
			TaskQueue: config.TaskQueue,
		},
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	}
}

func scheduleTimeZone(cfg config.Config) string {
	if cfg.ReportingTimezone != "" {
		return cfg.ReportingTimezone
	}
	return "America/New_York"
}

func notifyCron(cfg config.Config) string {
	if cfg.NotifyCron != "" {
		return cfg.NotifyCron
	}
	return "39 7,17,20 * * *"
}
