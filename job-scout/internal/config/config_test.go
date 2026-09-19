package config

import (
	"reflect"
	"testing"
)

func TestLoad_DebugFlag(t *testing.T) {
	t.Setenv("DEBUG", "")
	if Load().Debug {
		t.Error("DEBUG must be off by default")
	}

	t.Setenv("DEBUG", "1")
	if !Load().Debug {
		t.Error("DEBUG=1 must enable debug logging")
	}

	t.Setenv("DEBUG", "true")
	if Load().Debug {
		t.Error("only DEBUG=1 must enable debug logging")
	}
}

func TestLoad_OperatorUIDefaultsAndEnvironment(t *testing.T) {
	t.Setenv("TEMPORAL_UI_ADDRESS", "")
	t.Setenv("REPORTING_TIMEZONE", "")

	cfg := Load()
	assertConfigStringField(t, cfg, "TemporalUIAddress", "http://localhost:8080")
	assertConfigStringField(t, cfg, "ReportingTimezone", "America/New_York")

	t.Setenv("TEMPORAL_UI_ADDRESS", "http://temporal.example:9090")
	t.Setenv("REPORTING_TIMEZONE", "America/Chicago")
	cfg = Load()
	assertConfigStringField(t, cfg, "TemporalUIAddress", "http://temporal.example:9090")
	assertConfigStringField(t, cfg, "ReportingTimezone", "America/Chicago")
}

func assertConfigStringField(t *testing.T, cfg Config, name, want string) {
	t.Helper()
	field := reflect.ValueOf(cfg).FieldByName(name)
	if !field.IsValid() {
		t.Errorf("Config is missing field %s", name)
		return
	}
	if got := field.String(); got != want {
		t.Errorf("Config.%s = %q, want %q", name, got, want)
	}
}

func TestLoad_NotifyAndWebhookKnobDefaults(t *testing.T) {
	t.Setenv("WEBHOOK_BASE", "")
	t.Setenv("WEBHOOK_ID", "")
	t.Setenv("WEBHOOK_URL", "")
	t.Setenv("PIPEDREAM_API_TOKEN", "")
	t.Setenv("NOTIFY_MAX_JOBS", "")
	t.Setenv("NOTIFY_CLAIM_TIMEOUT_SECONDS", "")

	cfg := Load()
	if cfg.WebhookBase != "" {
		t.Errorf("WEBHOOK_BASE default = %q, want empty (sample is not a code default)", cfg.WebhookBase)
	}
	if cfg.WebhookID != "" {
		t.Errorf("WEBHOOK_ID default = %q, want empty", cfg.WebhookID)
	}
	if cfg.PipedreamAPIToken != "" {
		t.Errorf("PIPEDREAM_API_TOKEN default = %q, want empty", cfg.PipedreamAPIToken)
	}
	if cfg.NotifyMaxJobs != 25 {
		t.Errorf("NOTIFY_MAX_JOBS default = %d, want 25", cfg.NotifyMaxJobs)
	}
	if cfg.NotifyClaimTimeoutSeconds != 0 {
		t.Errorf("NOTIFY_CLAIM_TIMEOUT_SECONDS default = %d, want 0 (no auto-unstick)", cfg.NotifyClaimTimeoutSeconds)
	}
	if cfg.HTTPTimeoutSeconds != 30 {
		t.Errorf("HTTP_TIMEOUT_SECONDS default = %d, want 30 (webhook client reuses this)", cfg.HTTPTimeoutSeconds)
	}
}

func TestLoad_NotifyAndWebhookKnobsFromEnv(t *testing.T) {
	t.Setenv("WEBHOOK_BASE", "https://hooks.example/api/webhook")
	t.Setenv("WEBHOOK_ID", "hook-id")
	t.Setenv("WEBHOOK_URL", "http://unused.example/old")
	t.Setenv("PIPEDREAM_API_TOKEN", "pd-token")
	t.Setenv("NOTIFY_MAX_JOBS", "10")
	t.Setenv("NOTIFY_CLAIM_TIMEOUT_SECONDS", "90")
	t.Setenv("HTTP_TIMEOUT_SECONDS", "45")

	cfg := Load()
	if cfg.WebhookBase != "https://hooks.example/api/webhook" {
		t.Errorf("WEBHOOK_BASE = %q, want https://hooks.example/api/webhook", cfg.WebhookBase)
	}
	if cfg.WebhookID != "hook-id" {
		t.Errorf("WEBHOOK_ID = %q, want hook-id", cfg.WebhookID)
	}
	if cfg.PipedreamAPIToken != "pd-token" {
		t.Errorf("PIPEDREAM_API_TOKEN = %q, want pd-token", cfg.PipedreamAPIToken)
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

func TestLoad_PipedreamAPITokenStripsQuotes(t *testing.T) {
	t.Setenv("PIPEDREAM_API_TOKEN", `"quoted-token"`)
	cfg := Load()
	if cfg.PipedreamAPIToken != "quoted-token" {
		t.Errorf("quoted PIPEDREAM_API_TOKEN = %q, want quoted-token", cfg.PipedreamAPIToken)
	}
}

func TestWebhookTarget_IsTrimmedBaseOnly(t *testing.T) {
	tests := []struct {
		base       string
		id         string
		wantTarget string
		wantNotify string
	}{
		{
			base:       "https://hooks.example/api/webhook",
			id:         "hook-id",
			wantTarget: "https://hooks.example/api/webhook",
			wantNotify: "hook-id",
		},
		{
			base:       "https://hooks.example/api/webhook/",
			id:         "/hook-id",
			wantTarget: "https://hooks.example/api/webhook",
			wantNotify: "hook-id",
		},
		{
			base:       "https://hooks.example/api/webhook///",
			id:         "///hook",
			wantTarget: "https://hooks.example/api/webhook",
			wantNotify: "hook",
		},
	}
	for _, tc := range tests {
		cfg := Config{WebhookBase: tc.base, WebhookID: tc.id, WebhookURL: "http://unused.example"}
		got := cfg.WebhookTarget()
		if got != tc.wantTarget {
			t.Errorf("WebhookTarget(%q) = %q, want %q", tc.base, got, tc.wantTarget)
		}
		if notify := cfg.WebhookNotify(); notify != tc.wantNotify {
			t.Errorf("WebhookNotify(%q) = %q, want %q", tc.id, notify, tc.wantNotify)
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

func TestLoad_ScheduleSecondsFromProductEnv(t *testing.T) {
	t.Setenv("SCRAPE_SCHEDULE_SECONDS", "60*5")
	t.Setenv("NOTIFY_SCHEDULE_SECONDS", "60 * 10")

	cfg := Load()
	if cfg.ScrapeScheduleSeconds != 300 {
		t.Errorf("SCRAPE_SCHEDULE_SECONDS 60*5 = %d, want 300 (must not fall back to 60)", cfg.ScrapeScheduleSeconds)
	}
	if cfg.NotifyScheduleSeconds != 600 {
		t.Errorf("NOTIFY_SCHEDULE_SECONDS 60 * 10 = %d, want 600", cfg.NotifyScheduleSeconds)
	}
}

func TestLoad_ScheduleSecondsInvalidProductUsesDefault(t *testing.T) {
	t.Setenv("SCRAPE_SCHEDULE_SECONDS", "60*")
	t.Setenv("NOTIFY_SCHEDULE_SECONDS", "60*0")

	cfg := Load()
	if cfg.ScrapeScheduleSeconds != 60 {
		t.Errorf("invalid SCRAPE_SCHEDULE_SECONDS = %d, want default 60", cfg.ScrapeScheduleSeconds)
	}
	if cfg.NotifyScheduleSeconds != 300 {
		t.Errorf("invalid NOTIFY_SCHEDULE_SECONDS = %d, want default 300", cfg.NotifyScheduleSeconds)
	}
}
