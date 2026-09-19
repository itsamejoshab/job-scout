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
	"remote_lie",
	"detail_failed",
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
}

// DashboardStats is the database-backed dashboard aggregate response payload.
type DashboardStats struct {
	Timezone  string                   `json:"timezone"`
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
			COUNT(*) FILTER (WHERE j.state = 'eligible') AS eligible,
			COUNT(*) FILTER (WHERE j.state = 'notifying') AS notifying,
			COUNT(*) FILTER (WHERE j.state = 'notified') AS notified,
			COUNT(*) FILTER (WHERE j.reject_reason = 'duplicate') AS duplicate_count,
			COUNT(*) FILTER (WHERE j.reject_reason = 'title_company') AS title_company_count,
			COUNT(*) FILTER (WHERE j.reject_reason = 'description') AS description_count,
			COUNT(*) FILTER (WHERE j.reject_reason = 'remote_lie') AS remote_lie_count,
			COUNT(*) FILTER (WHERE j.reject_reason = 'detail_failed') AS detail_failed_count
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
			p                                 DashboardProviderStats
			lastScraped, nextEligible         sql.NullTime
			pending, rejected, eligible       int
			notifying, notified               int
			duplicateCount, titleCompanyCount int
			descriptionCount, remoteLieCount  int
			detailFailedCount                 int
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
			&eligible,
			&notifying,
			&notified,
			&duplicateCount,
			&titleCompanyCount,
			&descriptionCount,
			&remoteLieCount,
			&detailFailedCount,
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
			JobStatePending:   pending,
			JobStateRejected:  rejected,
			JobStateEligible:  eligible,
			JobStateNotifying: notifying,
			JobStateNotified:  notified,
		}
		p.ByRejectReason = map[string]int{
			dashboardRejectReasons[0]: duplicateCount,
			dashboardRejectReasons[1]: titleCompanyCount,
			dashboardRejectReasons[2]: descriptionCount,
			dashboardRejectReasons[3]: remoteLieCount,
			dashboardRejectReasons[4]: detailFailedCount,
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
		)
		SELECT
			to_char(days.day, 'YYYY-MM-DD') AS day,
			(
				SELECT COUNT(*)
				FROM jobs AS j
				WHERE ((j.created_at AT TIME ZONE 'UTC') AT TIME ZONE $1)::date = days.day
			) AS total,
			(
				SELECT COUNT(*)
				FROM jobs AS j
				WHERE j.state = 'notified'
				  AND (j.state_changed_at AT TIME ZONE $1)::date = days.day
			) AS notified
		FROM days
		ORDER BY days.day
	`, timezone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []DashboardDailyPoint{}
	for rows.Next() {
		var point DashboardDailyPoint
		if err := rows.Scan(&point.Day, &point.Total, &point.Notified); err != nil {
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
