package db

import (
	"context"
	"database/sql"
	"time"
)

var dashboardRejectReasons = []string{
	"duplicate",
	"title_company",
	"description",
	"detail_failed",
	"unsupported_source",
	"remote_lie",
}

// DashboardProviderStats is one provider row in the aggregate dashboard view.
type DashboardProviderStats struct {
	JobSource             JobSource      `json:"job_source"`
	Enabled               bool           `json:"enabled"`
	ScrapeIntervalSeconds int            `json:"scrape_interval_seconds"`
	LastScrapedAt         *time.Time     `json:"last_scraped_at"`
	NextEligibleAt        *time.Time     `json:"next_eligible_at"`
	TotalJobs             int            `json:"total_jobs"`
	ByState               map[string]int `json:"by_state"`
	ByRejectReason        map[string]int `json:"by_reject_reason"`
}

// DashboardDailyPoint is one reporting-day bucket for created jobs and notified jobs.
type DashboardDailyPoint struct {
	Day      string `json:"day"`
	Total    int    `json:"total"`
	Notified int    `json:"notified"`
	Applied  int    `json:"applied"`
	Pending  int    `json:"pending"`
	Skipped  int    `json:"skipped"`
}

// DashboardStats is the database-backed dashboard aggregate response payload.
type DashboardStats struct {
	Timezone  string                   `json:"timezone"`
	Notified  int                      `json:"notified"`
	Providers []DashboardProviderStats `json:"providers"`
	Daily     []DashboardDailyPoint    `json:"daily"`
}

// GetDashboardStats loads per-provider totals and a zero-filled 14-day series.
func GetDashboardStats(ctx context.Context, database *sql.DB, timezone string) (DashboardStats, error) {
	resolvedTZ := resolveDashboardTimezone(timezone)
	stats := DashboardStats{Timezone: resolvedTZ}

	providers, err := loadDashboardProviders(ctx, database)
	if err != nil {
		return stats, err
	}
	daily, err := loadDashboardDaily(ctx, database, resolvedTZ)
	if err != nil {
		return stats, err
	}

	stats.Providers = providers
	stats.Daily = daily
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE notified_at IS NOT NULL`).Scan(&stats.Notified); err != nil {
		return stats, err
	}
	return stats, nil
}

func loadDashboardProviders(ctx context.Context, database *sql.DB) ([]DashboardProviderStats, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT
			s.job_source,
			s.enabled,
			s.scrape_interval_seconds,
			s.last_scraped_at,
			s.next_eligible_at,
			COUNT(j.id) AS total_jobs,
			COUNT(*) FILTER (WHERE j.state = 'pending') AS pending,
			COUNT(*) FILTER (WHERE j.state = 'rejected') AS rejected,
			COUNT(*) FILTER (WHERE j.state = 'needs_detail') AS needs_detail,
			COUNT(*) FILTER (WHERE j.state = 'ready') AS ready,
			COUNT(*) FILTER (WHERE j.state = 'applied') AS applied,
			COUNT(*) FILTER (WHERE j.state = 'dismissed') AS dismissed,
			COUNT(*) FILTER (WHERE j.reject_reason = 'duplicate') AS duplicate_count,
			COUNT(*) FILTER (WHERE j.reject_reason = 'title_company') AS title_company_count,
			COUNT(*) FILTER (WHERE j.reject_reason = 'description') AS description_count,
			COUNT(*) FILTER (WHERE j.reject_reason = 'detail_failed') AS detail_failed_count,
			COUNT(*) FILTER (WHERE j.reject_reason = 'unsupported_source') AS unsupported_source_count,
			COUNT(*) FILTER (WHERE j.reject_reason = 'remote_lie') AS remote_lie_count
		FROM scraper_settings AS s
		LEFT JOIN jobs AS j ON j.job_source = s.job_source
		GROUP BY
			s.job_source,
			s.enabled,
			s.scrape_interval_seconds,
			s.last_scraped_at,
			s.next_eligible_at
		ORDER BY s.job_source
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []DashboardProviderStats{}
	for rows.Next() {
		var (
			p                                                           DashboardProviderStats
			lastScraped, nextEligible                                   sql.NullTime
			pending, rejected, needsDetail, ready                       int
			applied, dismissed                                          int
			duplicateCount, titleCompanyCount                           int
			descriptionCount, detailFailedCount, unsupportedSourceCount int
			remoteLieCount                                              int
		)
		if err := rows.Scan(
			&p.JobSource,
			&p.Enabled,
			&p.ScrapeIntervalSeconds,
			&lastScraped,
			&nextEligible,
			&p.TotalJobs,
			&pending,
			&rejected,
			&needsDetail,
			&ready,
			&applied,
			&dismissed,
			&duplicateCount,
			&titleCompanyCount,
			&descriptionCount,
			&detailFailedCount,
			&unsupportedSourceCount,
			&remoteLieCount,
		); err != nil {
			return nil, err
		}

		if lastScraped.Valid {
			t := lastScraped.Time
			p.LastScrapedAt = &t
		}
		if nextEligible.Valid {
			t := nextEligible.Time
			p.NextEligibleAt = &t
		}

		p.ByState = map[string]int{
			JobStatePending:     pending,
			JobStateRejected:    rejected,
			JobStateNeedsDetail: needsDetail,
			JobStateReady:       ready,
			JobStateApplied:     applied,
			JobStateDismissed:   dismissed,
		}
		p.ByRejectReason = map[string]int{
			dashboardRejectReasons[0]: duplicateCount,
			dashboardRejectReasons[1]: titleCompanyCount,
			dashboardRejectReasons[2]: descriptionCount,
			dashboardRejectReasons[3]: detailFailedCount,
			dashboardRejectReasons[4]: unsupportedSourceCount,
			dashboardRejectReasons[5]: remoteLieCount,
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func loadDashboardDaily(ctx context.Context, database *sql.DB, timezone string) ([]DashboardDailyPoint, error) {
	rows, err := database.QueryContext(ctx, `
		WITH days AS (
			SELECT generate_series(
				(now() AT TIME ZONE $1)::date - interval '13 day',
				(now() AT TIME ZONE $1)::date,
				interval '1 day'
			)::date AS day
		),
		created AS (
			SELECT
				((created_at AT TIME ZONE 'UTC') AT TIME ZONE $1)::date AS day,
				COUNT(*) AS total,
				COUNT(*) FILTER (WHERE state = 'applied') AS applied,
				COUNT(*) FILTER (WHERE state IN ('pending', 'needs_detail', 'ready')) AS pending,
				COUNT(*) FILTER (WHERE state IN ('rejected', 'dismissed')) AS skipped
			FROM jobs
			GROUP BY 1
		),
		notified AS (
			SELECT
				(notified_at AT TIME ZONE $1)::date AS day,
				COUNT(*) AS notified
			FROM jobs
			WHERE notified_at IS NOT NULL
			GROUP BY 1
		)
		SELECT
			to_char(days.day, 'YYYY-MM-DD') AS day,
			COALESCE(created.total, 0) AS total,
			COALESCE(notified.notified, 0) AS notified,
			COALESCE(created.applied, 0) AS applied,
			COALESCE(created.pending, 0) AS pending,
			COALESCE(created.skipped, 0) AS skipped
		FROM days
		LEFT JOIN created ON created.day = days.day
		LEFT JOIN notified ON notified.day = days.day
		ORDER BY days.day
	`, timezone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []DashboardDailyPoint{}
	for rows.Next() {
		var point DashboardDailyPoint
		if err := rows.Scan(
			&point.Day,
			&point.Total,
			&point.Notified,
			&point.Applied,
			&point.Pending,
			&point.Skipped,
		); err != nil {
			return nil, err
		}
		out = append(out, point)
	}
	return out, rows.Err()
}

func resolveDashboardTimezone(timezone string) string {
	if loc, err := time.LoadLocation(timezone); err == nil {
		return loc.String()
	}
	return "America/New_York"
}
