package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
)

func TestNotificationSettings_GetAndPut(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}

	h := &Handler{
		DB: pool,
		Cfg: config.Config{
			WebhookBase: "https://hooks.example/api/webhook",
			WebhookID:   "hook-id",
		},
	}
	srv := NewServer("", h)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/notification-settings", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got notificationSettingsView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if !got.Enabled || !got.Configured || !got.Active || got.Reason != "" {
		t.Fatalf("GET configured+enabled = %+v", got)
	}

	body, err := json.Marshal(map[string]any{"enabled": false})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req = httptest.NewRequest(http.MethodPut, "/api/v0/notification-settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode PUT: %v", err)
	}
	if got.Enabled || !got.Configured || got.Active || got.Reason == "" {
		t.Fatalf("PUT disabled = %+v", got)
	}

	fake := &runTemporalFake{}
	h.Temporal = fake
	req = httptest.NewRequest(http.MethodPost, "/api/v0/notify", nil)
	rec = httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST notify status=%d body=%s", rec.Code, rec.Body.String())
	}
	var notifyBody map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &notifyBody); err != nil {
		t.Fatalf("decode notify: %v", err)
	}
	if notifyBody["status"] != "notifications_disabled" {
		t.Fatalf("notify status = %v, want notifications_disabled", notifyBody["status"])
	}
	if len(fake.starts) != 0 {
		t.Fatalf("toggle off must not start Temporal, starts=%d", len(fake.starts))
	}
}

func TestNotificationSettings_UnconfiguredReportsInactive(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}

	h := &Handler{DB: pool, Cfg: config.Config{}}
	srv := NewServer("", h)
	req := httptest.NewRequest(http.MethodGet, "/api/v0/notification-settings", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got notificationSettingsView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Enabled || got.Configured || got.Active || got.Reason == "" {
		t.Fatalf("unconfigured status = %+v", got)
	}
}

func TestNotificationSettings_PutInstallsAndStoresSchedule(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	schedules := newAPIScheduleFake()
	h := &Handler{
		DB:        pool,
		Schedules: schedules,
		Cfg: config.Config{
			WebhookBase:       "https://hooks.example/api/webhook",
			WebhookID:         "hook-id",
			ReportingTimezone: "America/Chicago",
		},
	}
	srv := NewServer("", h)
	body := []byte(`{
		"enabled": true,
		"schedule": {
			"mode": "interval",
			"interval_minutes": 5,
			"cron_pattern": "0 8 * * *",
			"silent_periods": [
				{"days": [1, 2, 3, 4, 5], "start": "22:00", "end": "07:00"}
			]
		}
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v0/notification-settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(schedules.creates) != 1 || schedules.creates[0].ID != "jobscout-notify" {
		t.Fatalf("schedule creates=%v, want jobscout-notify", schedules.ids())
	}

	var got notificationSettingsView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode PUT: %v", err)
	}
	if got.Schedule.Mode != "interval" || got.Schedule.IntervalMinutes != 5 {
		t.Fatalf("response schedule = %+v", got.Schedule)
	}
	if got.TimeZone != "America/Chicago" {
		t.Errorf("response timezone=%q, want America/Chicago", got.TimeZone)
	}
	stored, err := db.GetNotificationScheduleSettings(t.Context(), pool)
	if err != nil {
		t.Fatalf("GetNotificationScheduleSettings: %v", err)
	}
	if stored == nil || stored.IntervalMinutes != 5 || len(stored.SilentPeriods) != 1 {
		t.Fatalf("stored schedule = %+v", stored)
	}
}

func TestNotificationSettings_InvalidScheduleDoesNotTouchTemporal(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	schedules := newAPIScheduleFake()
	h := &Handler{DB: pool, Schedules: schedules}
	srv := NewServer("", h)
	body := []byte(`{
		"enabled": true,
		"schedule": {
			"mode": "cron",
			"interval_minutes": 15,
			"cron_pattern": "not a cron",
			"silent_periods": []
		}
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v0/notification-settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(schedules.creates) != 0 {
		t.Fatalf("invalid input created %d schedules", len(schedules.creates))
	}
}
