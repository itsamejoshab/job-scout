package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // keep IANA time zones available in the Alpine runtime image

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/scraper"
	"github.com/robfig/cron"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

const (
	ScrapeScheduleID = "jobscout-scrape"
	NotifyScheduleID = "jobscout-notify"
)

const (
	NotificationScheduleInterval = "interval"
	NotificationScheduleCron     = "cron"
	maxNotificationInterval      = 7 * 24 * 60
	maxSilentPeriods             = 20
)

// SilentPeriod suppresses notification runs on selected local weekdays.
// Days use Sunday=0 through Saturday=6. End is exclusive.
type SilentPeriod struct {
	Days  []int  `json:"days"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// NotificationSchedule is the operator-owned notification cadence.
type NotificationSchedule struct {
	Mode            string         `json:"mode"`
	IntervalMinutes int            `json:"interval_minutes"`
	CronPattern     string         `json:"cron_pattern"`
	SilentPeriods   []SilentPeriod `json:"silent_periods"`
}

// ScheduleStore is the Temporal schedule client surface API boot uses.
type ScheduleStore interface {
	Create(ctx context.Context, options client.ScheduleOptions) (client.ScheduleHandle, error)
	GetHandle(ctx context.Context, scheduleID string) client.ScheduleHandle
}

// EnsureSchedules creates or updates the scrape and notify schedules.
func EnsureSchedules(ctx context.Context, schedules ScheduleStore, cfg config.Config) error {
	return EnsureSchedulesWithNotification(ctx, schedules, cfg, DefaultNotificationSchedule(cfg))
}

// EnsureSchedulesWithNotification creates or updates both app schedules.
func EnsureSchedulesWithNotification(
	ctx context.Context,
	schedules ScheduleStore,
	cfg config.Config,
	notification NotificationSchedule,
) error {
	if schedules == nil {
		return fmt.Errorf("temporal schedule client is required")
	}
	notify, err := notificationScheduleOptions(cfg, notification)
	if err != nil {
		return err
	}
	for _, opts := range []client.ScheduleOptions{scrapeSchedule(cfg), notify} {
		if err := upsertSchedule(ctx, schedules, opts); err != nil {
			return err
		}
	}
	return nil
}

// EnsureNotificationSchedule creates or updates only the notification
// schedule. It is used when the operator saves notification settings.
func EnsureNotificationSchedule(
	ctx context.Context,
	schedules ScheduleStore,
	cfg config.Config,
	notification NotificationSchedule,
) error {
	if schedules == nil {
		return fmt.Errorf("temporal schedule client is required")
	}
	opts, err := notificationScheduleOptions(cfg, notification)
	if err != nil {
		return err
	}
	return upsertSchedule(ctx, schedules, opts)
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

func notificationScheduleOptions(
	cfg config.Config,
	settings NotificationSchedule,
) (client.ScheduleOptions, error) {
	settings, err := normalizeNotificationSchedule(settings)
	if err != nil {
		return client.ScheduleOptions{}, err
	}
	if _, err := time.LoadLocation(scheduleTimeZone(cfg)); err != nil {
		return client.ScheduleOptions{}, fmt.Errorf("invalid reporting timezone: %w", err)
	}
	spec := client.ScheduleSpec{
		TimeZoneName: scheduleTimeZone(cfg),
		Skip:         silentCalendars(settings.SilentPeriods),
	}
	if settings.Mode == NotificationScheduleInterval {
		spec.Intervals = []client.ScheduleIntervalSpec{{
			Every: time.Duration(settings.IntervalMinutes) * time.Minute,
		}}
	} else {
		spec.CronExpressions = []string{settings.CronPattern}
	}
	return client.ScheduleOptions{
		ID:   NotifyScheduleID,
		Spec: spec,
		Action: &client.ScheduleWorkflowAction{
			ID:        ScheduledNotifyWorkflowID,
			Workflow:  NotifyWorkflow,
			TaskQueue: config.TaskQueue,
		},
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	}, nil
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

// DefaultNotificationSchedule preserves the environment-based cadence until
// the operator saves a schedule in Settings.
func DefaultNotificationSchedule(cfg config.Config) NotificationSchedule {
	return NotificationSchedule{
		Mode:            NotificationScheduleCron,
		IntervalMinutes: 15,
		CronPattern:     notifyCron(cfg),
		SilentPeriods:   []SilentPeriod{},
	}
}

// NormalizeNotificationSchedule validates and canonicalizes API input.
func NormalizeNotificationSchedule(settings NotificationSchedule) (NotificationSchedule, error) {
	return normalizeNotificationSchedule(settings)
}

func normalizeNotificationSchedule(settings NotificationSchedule) (NotificationSchedule, error) {
	settings.Mode = strings.TrimSpace(strings.ToLower(settings.Mode))
	settings.CronPattern = strings.TrimSpace(settings.CronPattern)
	if settings.IntervalMinutes == 0 {
		settings.IntervalMinutes = 15
	}
	switch settings.Mode {
	case NotificationScheduleInterval:
		if settings.IntervalMinutes < 1 || settings.IntervalMinutes > maxNotificationInterval {
			return NotificationSchedule{}, fmt.Errorf(
				"interval_minutes must be between 1 and %d",
				maxNotificationInterval,
			)
		}
	case NotificationScheduleCron:
		if _, err := cron.ParseStandard(settings.CronPattern); err != nil {
			return NotificationSchedule{}, fmt.Errorf("invalid five-field cron pattern: %w", err)
		}
	default:
		return NotificationSchedule{}, fmt.Errorf("mode must be interval or cron")
	}
	if settings.CronPattern == "" {
		settings.CronPattern = "39 7,17,20 * * *"
	}
	if len(settings.SilentPeriods) > maxSilentPeriods {
		return NotificationSchedule{}, fmt.Errorf(
			"silent_periods cannot contain more than %d entries",
			maxSilentPeriods,
		)
	}
	out := make([]SilentPeriod, 0, len(settings.SilentPeriods))
	for i, period := range settings.SilentPeriods {
		normalized, err := normalizeSilentPeriod(period)
		if err != nil {
			return NotificationSchedule{}, fmt.Errorf("silent_periods[%d]: %w", i, err)
		}
		out = append(out, normalized)
	}
	settings.SilentPeriods = out
	return settings, nil
}

func normalizeSilentPeriod(period SilentPeriod) (SilentPeriod, error) {
	start, err := parseClockMinute(period.Start)
	if err != nil {
		return SilentPeriod{}, fmt.Errorf("invalid start: %w", err)
	}
	end, err := parseClockMinute(period.End)
	if err != nil {
		return SilentPeriod{}, fmt.Errorf("invalid end: %w", err)
	}
	if start == end {
		return SilentPeriod{}, fmt.Errorf("start and end must differ")
	}
	seen := make(map[int]struct{}, len(period.Days))
	days := make([]int, 0, len(period.Days))
	for _, day := range period.Days {
		if day < 0 || day > 6 {
			return SilentPeriod{}, fmt.Errorf("days must use Sunday=0 through Saturday=6")
		}
		if _, ok := seen[day]; ok {
			continue
		}
		seen[day] = struct{}{}
		days = append(days, day)
	}
	if len(days) == 0 {
		return SilentPeriod{}, fmt.Errorf("select at least one day")
	}
	sort.Ints(days)
	return SilentPeriod{Days: days, Start: formatClockMinute(start), End: formatClockMinute(end)}, nil
}

func parseClockMinute(value string) (int, error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("use HH:MM")
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}

func formatClockMinute(value int) string {
	return fmt.Sprintf("%02d:%02d", value/60, value%60)
}

func silentCalendars(periods []SilentPeriod) []client.ScheduleCalendarSpec {
	var calendars []client.ScheduleCalendarSpec
	for _, period := range periods {
		start, _ := parseClockMinute(period.Start)
		end, _ := parseClockMinute(period.End)
		for _, day := range period.Days {
			if start < end {
				calendars = append(calendars, minuteRangeCalendars(day, start, end)...)
				continue
			}
			calendars = append(calendars, minuteRangeCalendars(day, start, 24*60)...)
			calendars = append(calendars, minuteRangeCalendars((day+1)%7, 0, end)...)
		}
	}
	return calendars
}

func minuteRangeCalendars(day, start, end int) []client.ScheduleCalendarSpec {
	if start >= end {
		return nil
	}
	var out []client.ScheduleCalendarSpec
	cursor := start
	if minute := cursor % 60; minute != 0 {
		stop := min(end, cursor+(60-minute))
		out = append(out, silentCalendar(day, cursor/60, cursor/60, minute, (stop-1)%60))
		cursor = stop
	}
	fullEnd := end - end%60
	if fullEnd > cursor {
		out = append(out, silentCalendar(day, cursor/60, fullEnd/60-1, 0, 59))
		cursor = fullEnd
	}
	if cursor < end {
		out = append(out, silentCalendar(day, cursor/60, cursor/60, 0, (end-1)%60))
	}
	return out
}

func silentCalendar(day, startHour, endHour, startMinute, endMinute int) client.ScheduleCalendarSpec {
	return client.ScheduleCalendarSpec{
		Second:    []client.ScheduleRange{{Start: 0, End: 59}},
		Minute:    []client.ScheduleRange{{Start: startMinute, End: endMinute}},
		Hour:      []client.ScheduleRange{{Start: startHour, End: endHour}},
		DayOfWeek: []client.ScheduleRange{{Start: day}},
	}
}
