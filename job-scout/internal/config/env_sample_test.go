package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvSample_DocumentsEveryRuntimeKnob(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".env.sample"))
	if err != nil {
		t.Fatalf("read .env.sample: %v", err)
	}
	knobs := parseEnvAssignments(string(body))

	want := map[string]string{
		"POSTGRES_USER":                "job",
		"POSTGRES_PASSWORD":            "applicant",
		"POSTGRES_HOST":                "postgres",
		"POSTGRES_PORT":                "5432",
		"POSTGRES_DB":                  "jobsearch",
		"TEMPORAL_ADDRESS":             "temporal:7233",
		"TEMPORAL_UI_ADDRESS":          "http://localhost:8082",
		"REPORTING_TIMEZONE":           "America/New_York",
		"API_PORT":                     "8000",
		"WEBHOOK_BASE":                 "",
		"WEBHOOK_ID":                   "",
		"PIPEDREAM_API_TOKEN":          "",
		"DEBUG":                        "0",
		"SCRAPE_SCHEDULE_SECONDS":      "600",
		"NOTIFY_CRON":                  "39 7,17,20 * * *",
		"SCRAPE_ERROR_BACKOFF_SECONDS": "300",
		"NOTIFY_MAX_JOBS":              "25",
		"NOTIFY_CLAIM_TIMEOUT_SECONDS": "900",
		"HTTP_TIMEOUT_SECONDS":         "30",
		"APIFY_API_TOKEN":              "",
		"APIFY_MONTHLY_BUDGET_USD":     "1.00",
	}
	for key, val := range want {
		if knobs[key] != val {
			t.Errorf(".env.sample %s = %q, want %q", key, knobs[key], val)
		}
	}
	if _, ok := knobs["WEBHOOK_URL"]; ok {
		t.Error(".env.sample must not set WEBHOOK_URL as an active send-target knob")
	}
	if knobs["PIPEDREAM_API_TOKEN"] != "" {
		t.Error(".env.sample PIPEDREAM_API_TOKEN must be an empty placeholder")
	}
	if _, ok := knobs["CLIENT_ID"]; ok {
		t.Error(".env.sample must not set CLIENT_ID")
	}
	if _, ok := knobs["CLIENT_SECRET"]; ok {
		t.Error(".env.sample must not set CLIENT_SECRET")
	}
	if _, ok := knobs["OAUTH_TOKEN_URL"]; ok {
		t.Error(".env.sample must not set OAUTH_TOKEN_URL")
	}
	if knobs["APIFY_API_TOKEN"] != "" {
		t.Error(".env.sample APIFY_API_TOKEN must be an empty placeholder")
	}
	if knobs["APIFY_MONTHLY_BUDGET_USD"] != "1.00" {
		t.Errorf(".env.sample APIFY_MONTHLY_BUDGET_USD = %q, want 1.00", knobs["APIFY_MONTHLY_BUDGET_USD"])
	}
	if _, ok := knobs["APIFY_USER_ID"]; ok {
		t.Error(".env.sample must not set APIFY_USER_ID")
	}
}

func parseEnvAssignments(body string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	return out
}
