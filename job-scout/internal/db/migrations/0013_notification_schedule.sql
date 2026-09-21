-- UI-owned notification cadence. A missing row keeps NOTIFY_CRON as the
-- default for existing installs.

CREATE TABLE IF NOT EXISTS notification_schedule_settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    mode TEXT NOT NULL CHECK (mode IN ('interval', 'cron')),
    interval_minutes INTEGER NOT NULL CHECK (interval_minutes BETWEEN 1 AND 10080),
    cron_pattern TEXT NOT NULL,
    silent_periods JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
