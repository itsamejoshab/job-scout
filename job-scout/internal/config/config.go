package config

import (
	"fmt"
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

	TemporalAddress string

	APIPort     string
	ProjectName string
	Version     string
	LogLevel    string

	WebhookID   string
	WebhookURL  string
	WebhookBase string

	HTTPTimeoutSeconds        int
	ScrapeErrorBackoffSeconds int
	NotifyMaxJobs             int
	NotifyClaimTimeoutSeconds int
	ScrapeScheduleSeconds     int
	NotifyScheduleSeconds     int
}

// WebhookTarget joins WEBHOOK_BASE and WEBHOOK_ID. It is the send URL.
func (c Config) WebhookTarget() string {
	base := strings.TrimRight(c.WebhookBase, "/")
	id := strings.TrimLeft(c.WebhookID, "/")
	if base == "" && id == "" {
		return ""
	}
	return base + "/" + id
}

const TaskQueue = "main-task-queue"

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
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func getenvIntAllowZero(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

func Load() Config {
	return Config{
		PostgresUser:              getenv("POSTGRES_USER", "default_user"),
		PostgresPassword:          getenv("POSTGRES_PASSWORD", "default_pass"),
		PostgresHost:              getenv("POSTGRES_HOST", "postgres"),
		PostgresPort:              getenv("POSTGRES_PORT", "5432"),
		PostgresDB:                getenv("POSTGRES_DB", "jobdb"),
		TemporalAddress:           getenv("TEMPORAL_ADDRESS", "temporal:7233"),
		APIPort:                   getenv("API_PORT", "8000"),
		ProjectName:               getenv("PROJECT_NAME", "Job-Scout Service"),
		Version:                   getenv("VERSION", "1.0.0"),
		LogLevel:                  getenv("LOG_LEVEL", "INFO"),
		WebhookID:                 getenv("WEBHOOK_ID", ""),
		WebhookURL:                getenv("WEBHOOK_URL", ""),
		WebhookBase:               getenv("WEBHOOK_BASE", ""),
		HTTPTimeoutSeconds:        getenvInt("HTTP_TIMEOUT_SECONDS", 30),
		ScrapeErrorBackoffSeconds: getenvInt("SCRAPE_ERROR_BACKOFF_SECONDS", 300),
		NotifyMaxJobs:             getenvInt("NOTIFY_MAX_JOBS", 25),
		NotifyClaimTimeoutSeconds: getenvIntAllowZero("NOTIFY_CLAIM_TIMEOUT_SECONDS", 0),
		ScrapeScheduleSeconds:     getenvInt("SCRAPE_SCHEDULE_SECONDS", 60),
		NotifyScheduleSeconds:     getenvInt("NOTIFY_SCHEDULE_SECONDS", 300),
	}
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
