package db

import (
	"context"
	"database/sql"
	"encoding/json"
)

// SilentPeriod suppresses scheduled notification runs during a local-time
// window. Days use Sunday=0 through Saturday=6.
type SilentPeriod struct {
	Days  []int  `json:"days"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// NotificationScheduleSettings is the UI-owned notification cadence.
type NotificationScheduleSettings struct {
	Mode            string         `json:"mode"`
	IntervalMinutes int            `json:"interval_minutes"`
	CronPattern     string         `json:"cron_pattern"`
	SilentPeriods   []SilentPeriod `json:"silent_periods"`
}

// GetNotificationScheduleSettings returns nil until the operator saves a
// schedule. This lets existing installs continue to use NOTIFY_CRON.
func GetNotificationScheduleSettings(
	ctx context.Context,
	database *sql.DB,
) (*NotificationScheduleSettings, error) {
	var (
		settings NotificationScheduleSettings
		silent   []byte
	)
	err := database.QueryRowContext(ctx, `
		SELECT mode, interval_minutes, cron_pattern, silent_periods
		FROM notification_schedule_settings
		WHERE singleton = TRUE
	`).Scan(
		&settings.Mode,
		&settings.IntervalMinutes,
		&settings.CronPattern,
		&silent,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(silent, &settings.SilentPeriods); err != nil {
		return nil, err
	}
	if settings.SilentPeriods == nil {
		settings.SilentPeriods = []SilentPeriod{}
	}
	return &settings, nil
}

// ReplaceNotificationScheduleSettings stores the cadence that was installed
// in Temporal.
func ReplaceNotificationScheduleSettings(
	ctx context.Context,
	database *sql.DB,
	settings NotificationScheduleSettings,
) (*NotificationScheduleSettings, error) {
	if settings.SilentPeriods == nil {
		settings.SilentPeriods = []SilentPeriod{}
	}
	silent, err := json.Marshal(settings.SilentPeriods)
	if err != nil {
		return nil, err
	}
	_, err = database.ExecContext(ctx, `
		INSERT INTO notification_schedule_settings (
			singleton, mode, interval_minutes, cron_pattern, silent_periods
		)
		VALUES (TRUE, $1, $2, $3, $4::jsonb)
		ON CONFLICT (singleton) DO UPDATE SET
			mode = EXCLUDED.mode,
			interval_minutes = EXCLUDED.interval_minutes,
			cron_pattern = EXCLUDED.cron_pattern,
			silent_periods = EXCLUDED.silent_periods,
			updated_at = now()
	`, settings.Mode, settings.IntervalMinutes, settings.CronPattern, silent)
	if err != nil {
		return nil, err
	}
	return GetNotificationScheduleSettings(ctx, database)
}
