package db

import (
	"reflect"
	"testing"

	"github.com/jobscout/jobscout/internal/db/pgtest"
)

func TestNotificationScheduleSettings_MissingThenReplace(t *testing.T) {
	pool := pgtest.Open(t)
	if err := Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	got, err := GetNotificationScheduleSettings(t.Context(), pool)
	if err != nil {
		t.Fatalf("GetNotificationScheduleSettings: %v", err)
	}
	if got != nil {
		t.Fatalf("new database schedule = %+v, want nil for config fallback", got)
	}

	want := NotificationScheduleSettings{
		Mode:            "interval",
		IntervalMinutes: 5,
		CronPattern:     "*/5 * * * *",
		SilentPeriods: []SilentPeriod{{
			Days:  []int{1, 2, 3, 4, 5},
			Start: "22:00",
			End:   "07:00",
		}},
	}
	got, err = ReplaceNotificationScheduleSettings(t.Context(), pool, want)
	if err != nil {
		t.Fatalf("ReplaceNotificationScheduleSettings: %v", err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("stored schedule = %+v, want %+v", *got, want)
	}

	want.Mode = "cron"
	want.CronPattern = "0 8 * * 1-5"
	got, err = ReplaceNotificationScheduleSettings(t.Context(), pool, want)
	if err != nil {
		t.Fatalf("second ReplaceNotificationScheduleSettings: %v", err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("updated schedule = %+v, want %+v", *got, want)
	}
}
