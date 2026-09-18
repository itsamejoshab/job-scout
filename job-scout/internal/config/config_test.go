package config

import "testing"

func TestLoad_NotifyAndWebhookKnobDefaults(t *testing.T) {
	t.Setenv("WEBHOOK_BASE", "")
	t.Setenv("WEBHOOK_ID", "")
	t.Setenv("WEBHOOK_URL", "")
	t.Setenv("NOTIFY_MAX_JOBS", "")
	t.Setenv("NOTIFY_CLAIM_TIMEOUT_SECONDS", "")

	cfg := Load()
	if cfg.WebhookBase != "" {
		t.Errorf("WEBHOOK_BASE default = %q, want empty (sample is not a code default)", cfg.WebhookBase)
	}
	if cfg.WebhookID != "" {
		t.Errorf("WEBHOOK_ID default = %q, want empty", cfg.WebhookID)
	}
	if cfg.NotifyMaxJobs != 25 {
		t.Errorf("NOTIFY_MAX_JOBS default = %d, want 25", cfg.NotifyMaxJobs)
	}
	if cfg.NotifyClaimTimeoutSeconds != 0 {
		t.Errorf("NOTIFY_CLAIM_TIMEOUT_SECONDS default = %d, want 0 (no auto-unstick)", cfg.NotifyClaimTimeoutSeconds)
	}
	if cfg.HTTPTimeoutSeconds != 30 {
		t.Errorf("HTTP_TIMEOUT_SECONDS default = %d, want 30 (Home Assistant client reuses this)", cfg.HTTPTimeoutSeconds)
	}
}

func TestLoad_NotifyAndWebhookKnobsFromEnv(t *testing.T) {
	t.Setenv("WEBHOOK_BASE", "https://hooks.example/api/webhook")
	t.Setenv("WEBHOOK_ID", "nabu-id")
	t.Setenv("WEBHOOK_URL", "http://unused.example/old")
	t.Setenv("NOTIFY_MAX_JOBS", "10")
	t.Setenv("NOTIFY_CLAIM_TIMEOUT_SECONDS", "90")
	t.Setenv("HTTP_TIMEOUT_SECONDS", "45")

	cfg := Load()
	if cfg.WebhookBase != "https://hooks.example/api/webhook" {
		t.Errorf("WEBHOOK_BASE = %q, want https://hooks.example/api/webhook", cfg.WebhookBase)
	}
	if cfg.WebhookID != "nabu-id" {
		t.Errorf("WEBHOOK_ID = %q, want nabu-id", cfg.WebhookID)
	}
	if cfg.NotifyMaxJobs != 10 {
		t.Errorf("NOTIFY_MAX_JOBS = %d, want 10", cfg.NotifyMaxJobs)
	}
	if cfg.NotifyClaimTimeoutSeconds != 90 {
		t.Errorf("NOTIFY_CLAIM_TIMEOUT_SECONDS = %d, want 90", cfg.NotifyClaimTimeoutSeconds)
	}
	if cfg.HTTPTimeoutSeconds != 45 {
		t.Errorf("HTTP_TIMEOUT_SECONDS = %d, want 45", cfg.HTTPTimeoutSeconds)
	}
	if target := cfg.WebhookTarget(); target == cfg.WebhookURL {
		t.Errorf("WebhookTarget must not use WEBHOOK_URL as the send target, got %q", target)
	}
}

func TestWebhookTarget_JoinsTrimmedBaseAndID(t *testing.T) {
	tests := []struct {
		base string
		id   string
		want string
	}{
		{
			base: "http://192.168.1.222:8123/api/webhook",
			id:   "allenjobhit",
			want: "http://192.168.1.222:8123/api/webhook/allenjobhit",
		},
		{
			base: "http://192.168.1.222:8123/api/webhook/",
			id:   "/allenjobhit",
			want: "http://192.168.1.222:8123/api/webhook/allenjobhit",
		},
		{
			base: "https://nabu.example/api/webhook///",
			id:   "///hook",
			want: "https://nabu.example/api/webhook/hook",
		},
	}
	for _, tc := range tests {
		cfg := Config{WebhookBase: tc.base, WebhookID: tc.id, WebhookURL: "http://unused.example"}
		got := cfg.WebhookTarget()
		if got != tc.want {
			t.Errorf("WebhookTarget(%q, %q) = %q, want %q", tc.base, tc.id, got, tc.want)
		}
		if got == cfg.WebhookURL {
			t.Errorf("WebhookTarget used WEBHOOK_URL %q", cfg.WebhookURL)
		}
	}
}

func TestLoad_HTTPTimeoutAndScrapeBackoffDefaults(t *testing.T) {
	t.Setenv("HTTP_TIMEOUT_SECONDS", "")
	t.Setenv("SCRAPE_ERROR_BACKOFF_SECONDS", "")

	cfg := Load()
	if cfg.HTTPTimeoutSeconds != 30 {
		t.Errorf("HTTP_TIMEOUT_SECONDS default = %d, want 30", cfg.HTTPTimeoutSeconds)
	}
	if cfg.ScrapeErrorBackoffSeconds != 300 {
		t.Errorf("SCRAPE_ERROR_BACKOFF_SECONDS default = %d, want 300", cfg.ScrapeErrorBackoffSeconds)
	}
}

func TestLoad_HTTPTimeoutAndScrapeBackoffFromEnv(t *testing.T) {
	t.Setenv("HTTP_TIMEOUT_SECONDS", "45")
	t.Setenv("SCRAPE_ERROR_BACKOFF_SECONDS", "120")

	cfg := Load()
	if cfg.HTTPTimeoutSeconds != 45 {
		t.Errorf("HTTP_TIMEOUT_SECONDS = %d, want 45", cfg.HTTPTimeoutSeconds)
	}
	if cfg.ScrapeErrorBackoffSeconds != 120 {
		t.Errorf("SCRAPE_ERROR_BACKOFF_SECONDS = %d, want 120", cfg.ScrapeErrorBackoffSeconds)
	}
}

func TestLoad_ScheduleSecondsDefaults(t *testing.T) {
	t.Setenv("SCRAPE_SCHEDULE_SECONDS", "")
	t.Setenv("NOTIFY_SCHEDULE_SECONDS", "")

	cfg := Load()
	if cfg.ScrapeScheduleSeconds != 60 {
		t.Errorf("SCRAPE_SCHEDULE_SECONDS default = %d, want 60", cfg.ScrapeScheduleSeconds)
	}
	if cfg.NotifyScheduleSeconds != 300 {
		t.Errorf("NOTIFY_SCHEDULE_SECONDS default = %d, want 300", cfg.NotifyScheduleSeconds)
	}
}

func TestLoad_ScheduleSecondsFromEnv(t *testing.T) {
	t.Setenv("SCRAPE_SCHEDULE_SECONDS", "90")
	t.Setenv("NOTIFY_SCHEDULE_SECONDS", "450")

	cfg := Load()
	if cfg.ScrapeScheduleSeconds != 90 {
		t.Errorf("SCRAPE_SCHEDULE_SECONDS = %d, want 90", cfg.ScrapeScheduleSeconds)
	}
	if cfg.NotifyScheduleSeconds != 450 {
		t.Errorf("NOTIFY_SCHEDULE_SECONDS = %d, want 450", cfg.NotifyScheduleSeconds)
	}
}
