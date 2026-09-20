package config

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// Config is the single source of truth for runtime configuration.
// Everything is sourced from the environment with sensible defaults so the
// DB credentials live in exactly one place (unlike the old Python app which
// hardcoded them in three).
type Config struct {
	PostgresUser     string
	PostgresPassword string
	PostgresHost     string
	PostgresPort     string
	PostgresDB       string

	TemporalAddress   string
	TemporalUIAddress string
	ReportingTimezone string

	APIPort     string
	ProjectName string
	Version     string
	LogLevel    string
	Debug       bool

	WebhookID   string
	WebhookURL  string
	WebhookBase string

	PipedreamAPIToken string

	HTTPTimeoutSeconds        int
	ScrapeErrorBackoffSeconds int
	NotifyMaxJobs             int
	NotifyClaimTimeoutSeconds int
	ScrapeScheduleSeconds     int
	NotifyCron                string
}

// WebhookTarget is WEBHOOK_BASE with trailing slashes removed. It is the send URL.
func (c Config) WebhookTarget() string {
	return strings.TrimRight(c.WebhookBase, "/")
}

// WebhookNotify is WEBHOOK_ID with leading slashes removed. It is the JSON notify value.
func (c Config) WebhookNotify() string {
	return strings.TrimLeft(c.WebhookID, "/")
}

const TaskQueue = "main-task-queue"

func trimEnvQuotes(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, ok := parseIntExpr(v)
	if !ok || n <= 0 {
		return def
	}
	return n
}

func getenvIntAllowZero(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, ok := parseIntExpr(v)
	if !ok || n < 0 {
		return def
	}
	return n
}

func getenvBoolFlag(key string) bool {
	return strings.TrimSpace(os.Getenv(key)) == "1"
}

// parseIntExpr reads a decimal integer or a product of positive integers
// such as 60*5 or 60 * 5. Invalid text is not an error here: callers use
// the configured default.
func parseIntExpr(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if !strings.Contains(v, "*") {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	product := 1
	for _, part := range strings.Split(v, "*") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n <= 0 {
			return 0, false
		}
		if product > math.MaxInt/n {
			return 0, false
		}
		product *= n
	}
	return product, true
}

func Load() Config {
	cfg := Config{
		PostgresUser:              getenv("POSTGRES_USER", "default_user"),
		PostgresPassword:          getenv("POSTGRES_PASSWORD", "default_pass"),
		PostgresHost:              getenv("POSTGRES_HOST", "postgres"),
		PostgresPort:              getenv("POSTGRES_PORT", "5432"),
		PostgresDB:                getenv("POSTGRES_DB", "jobdb"),
		TemporalAddress:           getenv("TEMPORAL_ADDRESS", "temporal:7233"),
		TemporalUIAddress:         getenv("TEMPORAL_UI_ADDRESS", "http://localhost:8082"),
		ReportingTimezone:         getenv("REPORTING_TIMEZONE", "America/New_York"),
		APIPort:                   getenv("API_PORT", "8000"),
		ProjectName:               getenv("PROJECT_NAME", "JobScout"),
		Version:                   getenv("VERSION", "1.0.0"),
		LogLevel:                  getenv("LOG_LEVEL", "INFO"),
		Debug:                     getenvBoolFlag("DEBUG"),
		WebhookID:                 getenv("WEBHOOK_ID", ""),
		WebhookURL:                getenv("WEBHOOK_URL", ""),
		WebhookBase:               getenv("WEBHOOK_BASE", ""),
		PipedreamAPIToken:         trimEnvQuotes(getenv("PIPEDREAM_API_TOKEN", "")),
		HTTPTimeoutSeconds:        getenvInt("HTTP_TIMEOUT_SECONDS", 30),
		ScrapeErrorBackoffSeconds: getenvInt("SCRAPE_ERROR_BACKOFF_SECONDS", 300),
		NotifyMaxJobs:             getenvInt("NOTIFY_MAX_JOBS", 25),
		NotifyClaimTimeoutSeconds: getenvIntAllowZero("NOTIFY_CLAIM_TIMEOUT_SECONDS", 900),
		ScrapeScheduleSeconds:     getenvInt("SCRAPE_SCHEDULE_SECONDS", 600),
		NotifyCron:                getenv("NOTIFY_CRON", "39 7,17,20 * * *"),
	}
	return cfg
}

// DatabaseURL returns a pgx/stdlib compatible DSN.
func (c Config) DatabaseURL() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		c.PostgresUser, c.PostgresPassword, c.PostgresHost, c.PostgresPort, c.PostgresDB,
	)
}

// RedactedDatabaseURL returns host/db only, for the /config endpoint.
func (c Config) RedactedDatabaseURL() string {
	return fmt.Sprintf("%s:%s/%s", c.PostgresHost, c.PostgresPort, c.PostgresDB)
}
