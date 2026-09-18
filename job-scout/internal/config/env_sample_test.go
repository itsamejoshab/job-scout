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
	text := string(body)
	knobs := parseEnvAssignments(text)

	want := map[string]string{
		"POSTGRES_USER":                "job",
		"POSTGRES_PASSWORD":            "applicant",
		"POSTGRES_HOST":                "postgres",
		"POSTGRES_PORT":                "5432",
		"POSTGRES_DB":                  "jobsearch",
		"TEMPORAL_ADDRESS":             "temporal:7233",
		"API_PORT":                     "8000",
		"WEBHOOK_BASE":                 "http://192.168.1.222:8123/api/webhook",
		"WEBHOOK_ID":                   "allenjobhit",
		"SCRAPE_SCHEDULE_SECONDS":      "60",
		"NOTIFY_SCHEDULE_SECONDS":      "300",
		"SCRAPE_ERROR_BACKOFF_SECONDS": "300",
		"NOTIFY_MAX_JOBS":              "25",
		"NOTIFY_CLAIM_TIMEOUT_SECONDS": "0",
		"HTTP_TIMEOUT_SECONDS":         "30",
	}
	for key, val := range want {
		if knobs[key] != val {
			t.Errorf(".env.sample %s = %q, want %q", key, knobs[key], val)
		}
	}
	if _, ok := knobs["WEBHOOK_URL"]; ok {
		t.Error(".env.sample must not set WEBHOOK_URL as an active send-target knob")
	}
	if strings.Contains(text, "192.168.1.222") && knobs["WEBHOOK_BASE"] != want["WEBHOOK_BASE"] {
		t.Errorf("LAN webhook sample must be WEBHOOK_BASE=%s", want["WEBHOOK_BASE"])
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
