package db

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

const (
	JobStatePending     = "pending"
	JobStateRejected    = "rejected"
	JobStateNeedsDetail = "needs_detail"
	JobStateReady       = "ready"
	JobStateApplied     = "applied"
	JobStateDismissed   = "dismissed"
)

// InsertJobIfNew inserts a job unless one with the same trimmed job_url already
// exists. On conflict it ORs is_remote and leaves state unchanged.
func InsertJobIfNew(ctx context.Context, db *sql.DB, j Job) (bool, error) {
	if j.Date.IsZero() {
		j.Date = time.Now()
	}
	url := strings.TrimSpace(j.JobURL)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO jobs (job_source, title, company, description, location, date, job_url,
		                  new, duplicate, relevant, promising, notified, is_remote, search_context)
		VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE, FALSE, FALSE, FALSE, FALSE, $8, $9)
		ON CONFLICT (job_url) DO NOTHING
	`, j.JobSource, truncate(j.Title, 100), truncate(j.Company, 100), j.Description,
		truncate(j.Location, 100), j.Date, url, j.IsRemote, j.SearchContext)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()

	if _, err := tx.ExecContext(ctx, `
		UPDATE jobs SET
			is_remote = is_remote OR $1,
			search_context = CASE
				WHEN search_context = '' THEN $2
				ELSE search_context
			END
		WHERE job_url = $3
	`, j.IsRemote, j.SearchContext, url); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n > 0, nil
}

// ListJobs returns jobs with pagination.
func ListJobs(ctx context.Context, db *sql.DB, limit, offset int) ([]Job, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, job_source, title, company, description, location, date, job_url,
		       created_at, updated_at, new, duplicate, relevant, promising, notified,
		       state, reject_reason, is_remote, search_context, detail_attempts, state_changed_at,
		       notified_at, notify_claimed_at
		FROM jobs ORDER BY id LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := []Job{}
	for rows.Next() {
		var j Job
		if err := rows.Scan(
			&j.ID, &j.JobSource, &j.Title, &j.Company, &j.Description, &j.Location,
			&j.Date, &j.JobURL, &j.CreatedAt, &j.UpdatedAt, &j.New, &j.Duplicate,
			&j.Relevant, &j.Promising, &j.Notified, &j.State, &j.RejectReason,
			&j.IsRemote, &j.SearchContext, &j.DetailAttempts, &j.StateChangedAt,
			&j.NotifiedAt, &j.NotifyClaimedAt,
		); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// JobListFilter defines the pinned, filtered set used by the operator list.
type JobListFilter struct {
	State     string
	JobSource JobSource
	Query     string
	DateFrom  string
	DateTo    string
	LastHours int // relative window ending at AsOf; 0 means no relative filter
	AsOf      time.Time
	Timezone  string
	Limit     int
	Offset    int
}

// ListJobsPage returns one page and the total from the same pinned result set.
func ListJobsPage(ctx context.Context, database *sql.DB, filter JobListFilter) ([]Job, int, error) {
	const where = `
		FROM jobs
		WHERE created_at <= ($1::timestamptz AT TIME ZONE 'UTC')
		  AND ($2 = '' OR state = $2)
		  AND ($3 = '' OR job_source::text = $3)
		  AND ($4 = '' OR title ILIKE '%' || $4 || '%' OR company ILIKE '%' || $4 || '%')
		  AND ($5 = '' OR ((created_at AT TIME ZONE 'UTC') AT TIME ZONE $7)::date >= $5::date)
		  AND ($6 = '' OR ((created_at AT TIME ZONE 'UTC') AT TIME ZONE $7)::date <= $6::date)
		  AND ($8::int = 0 OR created_at > (($1::timestamptz AT TIME ZONE 'UTC') - make_interval(hours => $8::int)))`
	args := []any{
		filter.AsOf,
		filter.State,
		string(filter.JobSource),
		filter.Query,
		filter.DateFrom,
		filter.DateTo,
		filter.Timezone,
		filter.LastHours,
	}

	var total int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*)`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := database.QueryContext(
		ctx,
		`SELECT`+jobSelectColumns+where+`
		ORDER BY created_at DESC, id DESC
		LIMIT $9 OFFSET $10`,
		append(args, filter.Limit, filter.Offset)...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	jobs := []Job{}
	for rows.Next() {
		var job Job
		if err := scanJob(rows, &job); err != nil {
			return nil, 0, err
		}
		jobs = append(jobs, job)
	}
	return jobs, total, rows.Err()
}

// GetJob returns a complete job row by identifier.
func GetJob(ctx context.Context, database *sql.DB, id int64) (Job, error) {
	var job Job
	err := scanJob(database.QueryRowContext(
		ctx,
		`SELECT`+jobSelectColumns+` FROM jobs WHERE id = $1`,
		id,
	), &job)
	return job, err
}

// NextPendingJobID returns the oldest pending row that is not excluded.
func NextPendingJobID(ctx context.Context, database *sql.DB, skipIDs []int64) (int64, error) {
	return nextJobIDByState(ctx, database, JobStatePending, skipIDs)
}

// NextNeedsDetailJobID returns the oldest needs_detail row that is not excluded.
func NextNeedsDetailJobID(ctx context.Context, database *sql.DB, skipIDs []int64) (int64, error) {
	return nextJobIDByState(ctx, database, JobStateNeedsDetail, skipIDs)
}

func nextJobIDByState(ctx context.Context, database *sql.DB, state string, skipIDs []int64) (int64, error) {
	if skipIDs == nil {
		skipIDs = []int64{}
	}
	var id int64
	err := database.QueryRowContext(ctx, `
		SELECT id
		FROM jobs
		WHERE state = $1
		  AND id <> ALL($2)
		ORDER BY created_at ASC, id ASC
		LIMIT 1
	`, state, skipIDs).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// ListDuplicateCandidates returns the whole table for domain-level Unicode
// title+company comparison. State is intentionally not part of the query.
func ListDuplicateCandidates(ctx context.Context, database *sql.DB) ([]Job, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT`+jobSelectColumns+`
		FROM jobs
		ORDER BY created_at, id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := []Job{}
	for rows.Next() {
		var job Job
		if err := scanJob(rows, &job); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// JobStats holds aggregate counts for the /jobs/stats endpoint.
type JobStats struct {
	TotalJobs    int            `json:"total_jobs"`
	NewJobs      int            `json:"new_jobs"` // pending job count (legacy JSON name)
	RelevantJobs int            `json:"relevant_jobs"`
	ByState      map[string]int `json:"by_state"`
}

func GetJobStats(ctx context.Context, db *sql.DB) (JobStats, error) {
	s := JobStats{ByState: map[string]int{
		JobStatePending:     0,
		JobStateRejected:    0,
		JobStateNeedsDetail: 0,
		JobStateReady:       0,
		JobStateApplied:     0,
		JobStateDismissed:   0,
	}}
	var pending, rejected, needsDetail, ready, applied, dismissed int
	err := db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE state = 'pending'),
			COUNT(*) FILTER (WHERE relevant),
			COUNT(*) FILTER (WHERE state = 'pending'),
			COUNT(*) FILTER (WHERE state = 'rejected'),
			COUNT(*) FILTER (WHERE state = 'needs_detail'),
			COUNT(*) FILTER (WHERE state = 'ready'),
			COUNT(*) FILTER (WHERE state = 'applied'),
			COUNT(*) FILTER (WHERE state = 'dismissed')
		FROM jobs
	`).Scan(&s.TotalJobs, &s.NewJobs, &s.RelevantJobs,
		&pending, &rejected, &needsDetail, &ready, &applied, &dismissed)
	if err != nil {
		return s, err
	}
	s.ByState[JobStatePending] = pending
	s.ByState[JobStateRejected] = rejected
	s.ByState[JobStateNeedsDetail] = needsDetail
	s.ByState[JobStateReady] = ready
	s.ByState[JobStateApplied] = applied
	s.ByState[JobStateDismissed] = dismissed
	return s, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// ListJobsForNotify returns every job for duplicate checks and pending filtering.
func ListJobsForNotify(ctx context.Context, db *sql.DB) ([]Job, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, job_source, title, company, description, location, date, job_url,
		       created_at, updated_at, new, duplicate, relevant, promising, notified,
		       state, reject_reason, is_remote, search_context, detail_attempts, state_changed_at,
		       notified_at, notify_claimed_at
		FROM jobs ORDER BY created_at, id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := []Job{}
	for rows.Next() {
		var j Job
		if err := rows.Scan(
			&j.ID, &j.JobSource, &j.Title, &j.Company, &j.Description, &j.Location,
			&j.Date, &j.JobURL, &j.CreatedAt, &j.UpdatedAt, &j.New, &j.Duplicate,
			&j.Relevant, &j.Promising, &j.Notified, &j.State, &j.RejectReason,
			&j.IsRemote, &j.SearchContext, &j.DetailAttempts, &j.StateChangedAt,
			&j.NotifiedAt, &j.NotifyClaimedAt,
		); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// UpdateJobNotifyState persists a filter decision without deleting the row.
func UpdateJobNotifyState(ctx context.Context, db *sql.DB, id int64, state string, reason *string, description *string, detailAttempts int) error {
	_, err := db.ExecContext(ctx, `
		UPDATE jobs SET
			state = $1,
			reject_reason = $2,
			description = COALESCE($3, description),
			detail_attempts = $4,
			state_changed_at = CASE WHEN state IS DISTINCT FROM $1 THEN now() ELSE state_changed_at END,
			updated_at = now()
		WHERE id = $5
	`, state, reason, description, detailAttempts, id)
	return err
}

// ReEvaluateRejectedJobs sends rejected jobs back to pending so the next notify
// pass applies the current filters. Attempts reset so an exhausted description
// fetch is retried. Human-reviewed and ready rows are never touched.
func ReEvaluateRejectedJobs(ctx context.Context, database *sql.DB) (int64, error) {
	res, err := database.ExecContext(ctx, `
		UPDATE jobs SET
			state = $1,
			reject_reason = NULL,
			detail_attempts = 0,
			state_changed_at = now(),
			updated_at = now()
		WHERE state = $2
	`, JobStatePending, JobStateRejected)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

const jobSelectColumns = `
		id, job_source, title, company, description, location, date, job_url,
		created_at, updated_at, new, duplicate, relevant, promising, notified,
		state, reject_reason, is_remote, search_context, detail_attempts, state_changed_at,
		notified_at, notify_claimed_at`

func scanJob(sc interface{ Scan(...any) error }, j *Job) error {
	return sc.Scan(
		&j.ID, &j.JobSource, &j.Title, &j.Company, &j.Description, &j.Location,
		&j.Date, &j.JobURL, &j.CreatedAt, &j.UpdatedAt, &j.New, &j.Duplicate,
		&j.Relevant, &j.Promising, &j.Notified, &j.State, &j.RejectReason,
		&j.IsRemote, &j.SearchContext, &j.DetailAttempts, &j.StateChangedAt,
		&j.NotifiedAt, &j.NotifyClaimedAt,
	)
}

// ClaimReadyJobs marks and returns up to limit oldest ready, unemailed rows.
func ClaimReadyJobs(ctx context.Context, database *sql.DB, limit, timeoutSeconds int) ([]Job, error) {
	if limit <= 0 {
		return nil, nil
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT`+jobSelectColumns+`
		FROM jobs
		WHERE state = $1
		  AND notified_at IS NULL
		  AND (
		    ($3::int = 0 AND notify_claimed_at IS NULL)
		    OR ($3::int > 0 AND (
		      notify_claimed_at IS NULL
		      OR notify_claimed_at < now() - make_interval(secs => $3::int)
		    ))
		  )
		ORDER BY created_at ASC, id ASC
		LIMIT $2
		FOR UPDATE SKIP LOCKED
	`, JobStateReady, limit, timeoutSeconds)
	if err != nil {
		return nil, err
	}
	claimed := []Job{}
	for rows.Next() {
		var j Job
		if err := scanJob(rows, &j); err != nil {
			rows.Close()
			return nil, err
		}
		claimed = append(claimed, j)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if len(claimed) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return claimed, nil
	}

	ids := make([]int64, len(claimed))
	for i, j := range claimed {
		ids[i] = j.ID
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE jobs SET
			notify_claimed_at = now(),
			notified_at = now(),
			updated_at = now()
		WHERE id = ANY($1)
	`, ids); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for i := range claimed {
		now := time.Now()
		claimed[i].NotifiedAt = &now
		claimed[i].NotifyClaimedAt = &now
	}
	return claimed, nil
}

// ClearNotifyClaims makes a failed webhook batch available for a later claim.
func ClearNotifyClaims(ctx context.Context, database *sql.DB, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := database.ExecContext(ctx, `
		UPDATE jobs SET
			notified_at = NULL,
			notify_claimed_at = NULL,
			updated_at = now()
		WHERE id = ANY($1)
	`, ids)
	return err
}

// ReviewReadyJob applies a final human review action to a ready row.
func ReviewReadyJob(ctx context.Context, database *sql.DB, id int64, action string) (bool, error) {
	res, err := database.ExecContext(ctx, `
		UPDATE jobs SET state = $1, state_changed_at = now(), updated_at = now()
		WHERE id = $2 AND state = $3
	`, action, id, JobStateReady)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
