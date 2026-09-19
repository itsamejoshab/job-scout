package pipeline

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/scraper"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

var errScheduleCreate = errors.New("temporal schedule create failed")

func TestEnsureSchedules_UsesProductScheduleEnv(t *testing.T) {
	t.Setenv("SCRAPE_SCHEDULE_SECONDS", "60*5")
	t.Setenv("NOTIFY_SCHEDULE_SECONDS", "60*10")
	t.Setenv("NOTIFY_SCHEDULE_OFFSET_SECONDS", "")
	t.Setenv("NOTIFY_ACTIVE_START", "")
	t.Setenv("NOTIFY_ACTIVE_END", "")
	t.Setenv("REPORTING_TIMEZONE", "")
	cfg := config.Load()

	fake := newFakeSchedules()
	if err := EnsureSchedules(context.Background(), fake, cfg); err != nil {
		t.Fatalf("EnsureSchedules: %v", err)
	}
	byID := scheduleCreatesByID(t, fake.creates)
	assertIntervalSchedule(t, byID["jobscout-scrape"], 300*time.Second, 0, "America/New_York")
	assertIntervalSchedule(t, byID["jobscout-notify"], 600*time.Second, 540*time.Second, "America/New_York")
	assertNotifyQuietHours(t, byID["jobscout-notify"])
}

func TestEnsureSchedules_DefaultNotifyOffsetAndQuietHours(t *testing.T) {
	t.Setenv("SCRAPE_SCHEDULE_SECONDS", "")
	t.Setenv("NOTIFY_SCHEDULE_SECONDS", "")
	t.Setenv("NOTIFY_SCHEDULE_OFFSET_SECONDS", "")
	t.Setenv("NOTIFY_ACTIVE_START", "")
	t.Setenv("NOTIFY_ACTIVE_END", "")
	t.Setenv("REPORTING_TIMEZONE", "")
	cfg := config.Load()

	fake := newFakeSchedules()
	if err := EnsureSchedules(context.Background(), fake, cfg); err != nil {
		t.Fatalf("EnsureSchedules: %v", err)
	}
	byID := scheduleCreatesByID(t, fake.creates)
	assertIntervalSchedule(t, byID["jobscout-scrape"], 600*time.Second, 0, "America/New_York")
	assertIntervalSchedule(t, byID["jobscout-notify"], 600*time.Second, 540*time.Second, "America/New_York")
	assertNotifyQuietHours(t, byID["jobscout-notify"])
	assertScheduledActions(t, byID["jobscout-scrape"], byID["jobscout-notify"])
}

func TestEnsureSchedules_CreatesUTCIntervalSchedulesWithSkip(t *testing.T) {
	fake := newFakeSchedules()
	cfg := config.Config{
		ScrapeScheduleSeconds: 90,
		NotifyScheduleSeconds: 450,
	}

	if err := EnsureSchedules(context.Background(), fake, cfg); err != nil {
		t.Fatalf("EnsureSchedules: %v", err)
	}

	byID := scheduleCreatesByID(t, fake.creates)
	assertIntervalSchedule(t, byID["jobscout-scrape"], 90*time.Second, 0, "UTC")
	assertIntervalSchedule(t, byID["jobscout-notify"], 450*time.Second, 0, "UTC")
	if len(byID["jobscout-notify"].Spec.Skip) != 0 {
		t.Errorf("notify skip=%v, want none when the active window is unset", byID["jobscout-notify"].Spec.Skip)
	}
	assertScheduledActions(t, byID["jobscout-scrape"], byID["jobscout-notify"])
}

func TestEnsureSchedules_UpdatesExistingSchedulesIdempotently(t *testing.T) {
	fake := newFakeSchedules()
	first := config.Config{ScrapeScheduleSeconds: 60, NotifyScheduleSeconds: 300}
	if err := EnsureSchedules(context.Background(), fake, first); err != nil {
		t.Fatalf("first EnsureSchedules: %v", err)
	}
	if len(fake.creates) != 2 {
		t.Fatalf("first boot must create two schedules, got %d", len(fake.creates))
	}

	second := config.Config{ScrapeScheduleSeconds: 120, NotifyScheduleSeconds: 600}
	if err := EnsureSchedules(context.Background(), fake, second); err != nil {
		t.Fatalf("second EnsureSchedules: %v", err)
	}
	if len(fake.creates) != 2 {
		t.Errorf("second boot must not create extra schedules, creates=%d", len(fake.creates))
	}
	if len(fake.updates) != 2 {
		t.Fatalf("second boot must update both schedules, updates=%d", len(fake.updates))
	}
	byID := map[string]client.ScheduleOptions{}
	for _, u := range fake.updates {
		if u.schedule == nil || u.schedule.Spec == nil || u.schedule.Policy == nil {
			t.Fatalf("update %q missing spec/policy: %+v", u.id, u.schedule)
		}
		byID[u.id] = client.ScheduleOptions{
			ID:      u.id,
			Spec:    *u.schedule.Spec,
			Action:  u.schedule.Action,
			Overlap: u.schedule.Policy.Overlap,
		}
	}
	scrape, ok := byID["jobscout-scrape"]
	if !ok {
		t.Fatalf("second boot must update jobscout-scrape, updates=%v", updateIDs(fake.updates))
	}
	notify, ok := byID["jobscout-notify"]
	if !ok {
		t.Fatalf("second boot must update jobscout-notify, updates=%v", updateIDs(fake.updates))
	}
	assertIntervalSchedule(t, scrape, 120*time.Second, 0, "UTC")
	assertIntervalSchedule(t, notify, 600*time.Second, 0, "UTC")
	assertScheduledActions(t, scrape, notify)
}

func TestEnsureSchedules_CreateErrorStopsBoot(t *testing.T) {
	fake := newFakeSchedules()
	fake.createErr = errScheduleCreate
	err := EnsureSchedules(context.Background(), fake, config.Config{
		ScrapeScheduleSeconds: 60,
		NotifyScheduleSeconds: 300,
	})
	if err == nil {
		t.Fatal("EnsureSchedules must return a Temporal create error so API boot does not continue without schedules")
	}
}

func TestNotifySkipCalendars_DefaultMorningToEvening(t *testing.T) {
	got := notifySkipCalendars(config.Config{NotifyActiveStart: "07:30", NotifyActiveEnd: "21:00"})
	want := []client.ScheduleCalendarSpec{
		{Hour: []client.ScheduleRange{{Start: 0, End: 6}}},
		{Hour: []client.ScheduleRange{{Start: 7}}, Minute: []client.ScheduleRange{{Start: 0, End: 29}}},
		{Hour: []client.ScheduleRange{{Start: 21, End: 23}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("notify skip = %#v, want %#v", got, want)
	}
}

func TestNotifySkipCalendars_EmptyWindowIsNone(t *testing.T) {
	if got := notifySkipCalendars(config.Config{}); got != nil {
		t.Errorf("empty notify window skip = %#v, want none", got)
	}
}

func assertIntervalSchedule(t *testing.T, opts client.ScheduleOptions, every, offset time.Duration, tz string) {
	t.Helper()
	if opts.Spec.TimeZoneName != tz {
		t.Errorf("schedule %s timezone = %q, want %s", opts.ID, opts.Spec.TimeZoneName, tz)
	}
	if opts.Overlap != enumspb.SCHEDULE_OVERLAP_POLICY_SKIP {
		t.Errorf("schedule %s overlap = %v, want SKIP so a running tick is not stacked", opts.ID, opts.Overlap)
	}
	if len(opts.Spec.Calendars) != 0 || len(opts.Spec.CronExpressions) != 0 {
		t.Errorf("schedule %s must be interval-based, calendars=%d cron=%d", opts.ID, len(opts.Spec.Calendars), len(opts.Spec.CronExpressions))
	}
	if len(opts.Spec.Intervals) != 1 {
		t.Fatalf("schedule %s intervals=%v, want one", opts.ID, opts.Spec.Intervals)
	}
	if opts.Spec.Intervals[0].Every != every {
		t.Errorf("schedule %s interval = %s, want %s", opts.ID, opts.Spec.Intervals[0].Every, every)
	}
	if opts.Spec.Intervals[0].Offset != offset {
		t.Errorf("schedule %s offset = %s, want %s", opts.ID, opts.Spec.Intervals[0].Offset, offset)
	}
}

func assertNotifyQuietHours(t *testing.T, opts client.ScheduleOptions) {
	t.Helper()
	want := notifySkipCalendars(config.Config{NotifyActiveStart: "07:30", NotifyActiveEnd: "21:00"})
	if !reflect.DeepEqual(opts.Spec.Skip, want) {
		t.Errorf("notify skip = %#v, want %#v", opts.Spec.Skip, want)
	}
}

func assertScheduledActions(t *testing.T, scrape, notify client.ScheduleOptions) {
	t.Helper()
	scrapeAction := scheduleWorkflowAction(t, scrape)
	notifyAction := scheduleWorkflowAction(t, notify)
	if scrapeAction.ID != "jobscout-scrape-scheduled" {
		t.Errorf("scrape scheduled workflow ID = %q, want jobscout-scrape-scheduled", scrapeAction.ID)
	}
	if notifyAction.ID != "jobscout-notify-scheduled" {
		t.Errorf("notify scheduled workflow ID = %q, want jobscout-notify-scheduled", notifyAction.ID)
	}
	if scrapeAction.ID == ManualScrapeWorkflowID(time.Now()) {
		t.Error("scheduled scrape workflow ID must not be a manual scrape ID")
	}
	if notifyAction.ID == ManualNotifyWorkflowID(time.Now()) {
		t.Error("scheduled notify workflow ID must not be a manual notify ID")
	}
	if name := workflowFuncName(scrapeAction.Workflow); name != "ScrapeWorkflow" {
		t.Errorf("scrape schedule must start ScrapeWorkflow, got %s", name)
	}
	if name := workflowFuncName(notifyAction.Workflow); name != "NotifyWorkflow" {
		t.Errorf("notify schedule must start NotifyWorkflow, got %s", name)
	}
	if scrapeAction.TaskQueue != config.TaskQueue || notifyAction.TaskQueue != config.TaskQueue {
		t.Errorf("schedules must use task queue %q, scrape=%q notify=%q", config.TaskQueue, scrapeAction.TaskQueue, notifyAction.TaskQueue)
	}
	if len(scrapeAction.Args) != 1 {
		t.Fatalf("scheduled ScrapeWorkflow args=%v, want one TickInput", scrapeAction.Args)
	}
	in, ok := scrapeAction.Args[0].(scraper.TickInput)
	if !ok {
		t.Fatalf("scheduled ScrapeWorkflow arg type %T, want scraper.TickInput", scrapeAction.Args[0])
	}
	if in.Force {
		t.Error("scheduled scrape must not set force")
	}
	if len(notifyAction.Args) != 0 {
		t.Errorf("scheduled NotifyWorkflow args=%v, want none", notifyAction.Args)
	}
}

func scheduleCreatesByID(t *testing.T, creates []client.ScheduleOptions) map[string]client.ScheduleOptions {
	t.Helper()
	if len(creates) != 2 {
		t.Fatalf("EnsureSchedules must create scrape and notify schedules, got %d", len(creates))
	}
	out := map[string]client.ScheduleOptions{}
	for _, opts := range creates {
		out[opts.ID] = opts
	}
	if _, ok := out["jobscout-scrape"]; !ok {
		t.Fatalf("missing schedule ID jobscout-scrape, got %v", scheduleIDs(creates))
	}
	if _, ok := out["jobscout-notify"]; !ok {
		t.Fatalf("missing schedule ID jobscout-notify, got %v", scheduleIDs(creates))
	}
	return out
}

func scheduleWorkflowAction(t *testing.T, opts client.ScheduleOptions) *client.ScheduleWorkflowAction {
	t.Helper()
	action, ok := opts.Action.(*client.ScheduleWorkflowAction)
	if !ok {
		t.Fatalf("schedule %s action type %T, want *client.ScheduleWorkflowAction", opts.ID, opts.Action)
	}
	return action
}

func scheduleIDs(opts []client.ScheduleOptions) []string {
	ids := make([]string, len(opts))
	for i, o := range opts {
		ids[i] = o.ID
	}
	return ids
}

func updateIDs(updates []scheduleUpdate) []string {
	ids := make([]string, len(updates))
	for i, u := range updates {
		ids[i] = u.id
	}
	return ids
}

type scheduleUpdate struct {
	id       string
	schedule *client.Schedule
}

type fakeSchedules struct {
	creates   []client.ScheduleOptions
	existing  map[string]bool
	updates   []scheduleUpdate
	createErr error
}

func newFakeSchedules() *fakeSchedules {
	return &fakeSchedules{existing: map[string]bool{}}
}

func (f *fakeSchedules) Create(_ context.Context, options client.ScheduleOptions) (client.ScheduleHandle, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.existing[options.ID] {
		return nil, temporal.ErrScheduleAlreadyRunning
	}
	f.creates = append(f.creates, options)
	f.existing[options.ID] = true
	return &fakeHandle{id: options.ID, parent: f}, nil
}

func (f *fakeSchedules) GetHandle(_ context.Context, scheduleID string) client.ScheduleHandle {
	return &fakeHandle{id: scheduleID, parent: f}
}

type fakeHandle struct {
	id     string
	parent *fakeSchedules
}

func (h *fakeHandle) GetID() string { return h.id }

func (h *fakeHandle) Delete(context.Context) error { return nil }

func (h *fakeHandle) Backfill(context.Context, client.ScheduleBackfillOptions) error { return nil }

func (h *fakeHandle) Update(_ context.Context, options client.ScheduleUpdateOptions) error {
	in := client.ScheduleUpdateInput{
		Description: client.ScheduleDescription{
			Schedule: client.Schedule{
				Spec:   &client.ScheduleSpec{},
				Policy: &client.SchedulePolicies{},
				State:  &client.ScheduleState{},
			},
		},
	}
	out, err := options.DoUpdate(in)
	if err != nil {
		return err
	}
	h.parent.updates = append(h.parent.updates, scheduleUpdate{id: h.id, schedule: out.Schedule})
	return nil
}

func (h *fakeHandle) Describe(context.Context) (*client.ScheduleDescription, error) {
	return nil, nil
}

func (h *fakeHandle) Trigger(context.Context, client.ScheduleTriggerOptions) error { return nil }

func (h *fakeHandle) Pause(context.Context, client.SchedulePauseOptions) error { return nil }

func (h *fakeHandle) Unpause(context.Context, client.ScheduleUnpauseOptions) error { return nil }

func workflowFuncName(fn any) string {
	if s, ok := fn.(string); ok {
		return s
	}
	v := reflect.ValueOf(fn)
	if !v.IsValid() || v.Kind() != reflect.Func {
		return ""
	}
	name := runtime.FuncForPC(v.Pointer()).Name()
	name = strings.TrimSuffix(name, "-fm")
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return name
}
