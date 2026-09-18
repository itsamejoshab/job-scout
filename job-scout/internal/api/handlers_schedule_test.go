package api

import (
	"context"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/pipeline"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

func TestInstallSchedules_UsesHandlerConfigAtAPIBoot(t *testing.T) {
	fake := newAPIScheduleFake()
	h := &Handler{
		Schedules: fake,
		Cfg: config.Config{
			ScrapeScheduleSeconds: 90,
			NotifyScheduleSeconds: 450,
		},
	}
	if err := h.InstallSchedules(context.Background()); err != nil {
		t.Fatalf("InstallSchedules: %v", err)
	}
	if len(fake.creates) != 2 {
		t.Fatalf("API boot must create scrape and notify schedules, got %d", len(fake.creates))
	}
	byID := map[string]client.ScheduleOptions{}
	for _, opts := range fake.creates {
		byID[opts.ID] = opts
	}
	scrape, ok := byID["jobscout-scrape"]
	if !ok {
		t.Fatalf("API boot must install jobscout-scrape, ids=%v", fake.ids())
	}
	notify, ok := byID["jobscout-notify"]
	if !ok {
		t.Fatalf("API boot must install jobscout-notify, ids=%v", fake.ids())
	}
	if scrape.Overlap != enumspb.SCHEDULE_OVERLAP_POLICY_SKIP || notify.Overlap != enumspb.SCHEDULE_OVERLAP_POLICY_SKIP {
		t.Errorf("API boot schedules must skip overlap, scrape=%v notify=%v", scrape.Overlap, notify.Overlap)
	}
	if len(scrape.Spec.Intervals) != 1 || scrape.Spec.Intervals[0].Every != 90*time.Second {
		t.Errorf("API boot scrape interval = %v, want 90s from Handler.Cfg", scrape.Spec.Intervals)
	}
	if len(notify.Spec.Intervals) != 1 || notify.Spec.Intervals[0].Every != 450*time.Second {
		t.Errorf("API boot notify interval = %v, want 450s from Handler.Cfg", notify.Spec.Intervals)
	}
	scrapeAction, _ := scrape.Action.(*client.ScheduleWorkflowAction)
	notifyAction, _ := notify.Action.(*client.ScheduleWorkflowAction)
	if scrapeAction == nil || scrapeAction.ID != "jobscout-scrape-scheduled" {
		t.Errorf("API boot scrape workflow ID = %v, want jobscout-scrape-scheduled", scrape.Action)
	}
	if notifyAction == nil || notifyAction.ID != "jobscout-notify-scheduled" {
		t.Errorf("API boot notify workflow ID = %v, want jobscout-notify-scheduled", notify.Action)
	}
	if err := h.InstallSchedules(context.Background()); err != nil {
		t.Fatalf("second InstallSchedules: %v", err)
	}
	if len(fake.creates) != 2 {
		t.Errorf("API boot must be idempotent, creates=%d", len(fake.creates))
	}
	if fake.updates != 2 {
		t.Errorf("second API boot must update existing schedules, updates=%d", fake.updates)
	}
}

func TestInstallSchedules_NilStoreIsError(t *testing.T) {
	h := &Handler{Cfg: config.Config{ScrapeScheduleSeconds: 60, NotifyScheduleSeconds: 300}}
	if err := h.InstallSchedules(context.Background()); err == nil {
		t.Fatal("API boot must fail when Temporal schedule client is missing")
	}
}

type apiScheduleFake struct {
	creates  []client.ScheduleOptions
	existing map[string]bool
	updates  int
}

func newAPIScheduleFake() *apiScheduleFake {
	return &apiScheduleFake{existing: map[string]bool{}}
}

func (f *apiScheduleFake) ids() []string {
	ids := make([]string, len(f.creates))
	for i, o := range f.creates {
		ids[i] = o.ID
	}
	return ids
}

func (f *apiScheduleFake) Create(_ context.Context, options client.ScheduleOptions) (client.ScheduleHandle, error) {
	if f.existing[options.ID] {
		return nil, temporal.ErrScheduleAlreadyRunning
	}
	f.creates = append(f.creates, options)
	f.existing[options.ID] = true
	return &apiScheduleHandle{parent: f}, nil
}

func (f *apiScheduleFake) GetHandle(context.Context, string) client.ScheduleHandle {
	return &apiScheduleHandle{parent: f}
}

type apiScheduleHandle struct {
	parent *apiScheduleFake
}

func (h *apiScheduleHandle) GetID() string { return "" }

func (h *apiScheduleHandle) Delete(context.Context) error { return nil }

func (h *apiScheduleHandle) Backfill(context.Context, client.ScheduleBackfillOptions) error {
	return nil
}

func (h *apiScheduleHandle) Update(_ context.Context, options client.ScheduleUpdateOptions) error {
	out, err := options.DoUpdate(client.ScheduleUpdateInput{
		Description: client.ScheduleDescription{
			Schedule: client.Schedule{
				Spec:   &client.ScheduleSpec{},
				Policy: &client.SchedulePolicies{},
				State:  &client.ScheduleState{},
			},
		},
	})
	if err != nil {
		return err
	}
	if out != nil {
		h.parent.updates++
	}
	return nil
}

func (h *apiScheduleHandle) Describe(context.Context) (*client.ScheduleDescription, error) {
	return nil, nil
}

func (h *apiScheduleHandle) Trigger(context.Context, client.ScheduleTriggerOptions) error { return nil }

func (h *apiScheduleHandle) Pause(context.Context, client.SchedulePauseOptions) error { return nil }

func (h *apiScheduleHandle) Unpause(context.Context, client.ScheduleUnpauseOptions) error {
	return nil
}

// assert pipeline.ScheduleStore is the Handler field type (compile seam).
var _ pipeline.ScheduleStore = (*apiScheduleFake)(nil)
