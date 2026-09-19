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
			Intervals: []client.ScheduleIntervalSpec{{
				Every:  time.Duration(cfg.NotifyScheduleSeconds) * time.Second,
				Offset: time.Duration(cfg.NotifyScheduleOffsetSeconds) * time.Second,
			}},
			Skip:         notifySkipCalendars(cfg),
			TimeZoneName: scheduleTimeZone(cfg),
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
	return "UTC"
}

func notifySkipCalendars(cfg config.Config) []client.ScheduleCalendarSpec {
	sh, sm, eh, em, ok := cfg.NotifyActiveWindow()
	if !ok {
		return nil
	}
	var skip []client.ScheduleCalendarSpec
	if sh > 0 {
		skip = append(skip, skipWholeHours(0, sh-1))
	}
	if sm > 0 {
		skip = append(skip, skipPartialHour(sh, 0, sm-1))
	}
	if em == 0 {
		return append(skip, skipWholeHours(eh, 23))
	}
	skip = append(skip, skipPartialHour(eh, em, 59))
	if eh < 23 {
		skip = append(skip, skipWholeHours(eh+1, 23))
	}
	return skip
}

// skipWholeHours excludes every tick from the start of first to the end of last.
// Minute and second must be whole ranges: the SDK fills an unset field with 0,
// which would exclude only the top of each hour.
func skipWholeHours(first, last int) client.ScheduleCalendarSpec {
	return client.ScheduleCalendarSpec{
		Hour:   scheduleRange(first, last),
		Minute: scheduleRange(0, 59),
		Second: scheduleRange(0, 59),
	}
}

// skipPartialHour excludes minutes first through last of one hour.
func skipPartialHour(hour, first, last int) client.ScheduleCalendarSpec {
	return client.ScheduleCalendarSpec{
		Hour:   scheduleRange(hour, hour),
		Minute: scheduleRange(first, last),
		Second: scheduleRange(0, 59),
	}
}

func scheduleRange(first, last int) []client.ScheduleRange {
	return []client.ScheduleRange{{Start: first, End: last}}
}
