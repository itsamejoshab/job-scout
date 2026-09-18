package db

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

const (
	JobStatePending   = "pending"
	JobStateRejected  = "rejected"
	JobStateEligible  = "eligible"
	JobStateNotifying = "notifying"
	JobStateNotified  = "notified"
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
		                  new, duplicate, relevant, promising, notified, is_remote)
		VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE, FALSE, FALSE, FALSE, FALSE, $8)
		ON CONFLICT (job_url) DO NOTHING
	`, j.JobSource, truncate(j.Title, 100), truncate(j.Company, 100), j.Description,
		truncate(j.Location, 100), j.Date, url, j.IsRemote)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()

	if _, err := tx.ExecContext(ctx, `
		UPDATE jobs SET is_remote = is_remote OR $1 WHERE job_url = $2
	`, j.IsRemote, url); err != nil {
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
		       state, reject_reason, is_remote, detail_attempts, state_changed_at
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
			&j.IsRemote, &j.DetailAttempts, &j.StateChangedAt,
		); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
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
		JobStatePending:   0,
		JobStateRejected:  0,
		JobStateEligible:  0,
		JobStateNotifying: 0,
		JobStateNotified:  0,
	}}
	var pending, rejected, eligible, notifying, notified int
	err := db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE state = 'pending'),
			COUNT(*) FILTER (WHERE relevant),
			COUNT(*) FILTER (WHERE state = 'pending'),
			COUNT(*) FILTER (WHERE state = 'rejected'),
			COUNT(*) FILTER (WHERE state = 'eligible'),
			COUNT(*) FILTER (WHERE state = 'notifying'),
			COUNT(*) FILTER (WHERE state = 'notified')
		FROM jobs
	`).Scan(&s.TotalJobs, &s.NewJobs, &s.RelevantJobs,
		&pending, &rejected, &eligible, &notifying, &notified)
	if err != nil {
		return s, err
	}
	s.ByState[JobStatePending] = pending
	s.ByState[JobStateRejected] = rejected
	s.ByState[JobStateEligible] = eligible
	s.ByState[JobStateNotifying] = notifying
	s.ByState[JobStateNotified] = notified
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
		       state, reject_reason, is_remote, detail_attempts, state_changed_at
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
			&j.IsRemote, &j.DetailAttempts, &j.StateChangedAt,
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

const jobSelectColumns = `
		id, job_source, title, company, description, location, date, job_url,
		created_at, updated_at, new, duplicate, relevant, promising, notified,
		state, reject_reason, is_remote, detail_attempts, state_changed_at`

func scanJob(sc interface{ Scan(...any) error }, j *Job) error {
	return sc.Scan(
		&j.ID, &j.JobSource, &j.Title, &j.Company, &j.Description, &j.Location,
		&j.Date, &j.JobURL, &j.CreatedAt, &j.UpdatedAt, &j.New, &j.Duplicate,
		&j.Relevant, &j.Promising, &j.Notified, &j.State, &j.RejectReason,
		&j.IsRemote, &j.DetailAttempts, &j.StateChangedAt,
	)
}

// NotifyInventory is whole-table counts for message_to_send after claim.
type NotifyInventory struct {
	Total        int
	TitleCompany int
	Description  int
	RemoteLie    int
	Duplicate    int
	DetailFailed int
	Pending      int
	Eligible     int
	Notifying    int
	Notified     int
}

// ClaimEligibleJobs claims up to limit oldest eligible rows (created_at, id)
// with FOR UPDATE SKIP LOCKED and sets state=notifying.
func ClaimEligibleJobs(ctx context.Context, database *sql.DB, limit int) ([]Job, error) {
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
		ORDER BY created_at ASC, id ASC
		LIMIT $2
		FOR UPDATE SKIP LOCKED
	`, JobStateEligible, limit)
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
			state = $1,
			state_changed_at = CASE WHEN state IS DISTINCT FROM $1 THEN now() ELSE state_changed_at END,
			updated_at = now()
		WHERE id = ANY($2)
	`, JobStateNotifying, ids); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for i := range claimed {
		claimed[i].State = JobStateNotifying
	}
	return claimed, nil
}

// LoadNotifyInventory returns whole-table counts after claim and before POST.
func LoadNotifyInventory(ctx context.Context, database *sql.DB) (NotifyInventory, error) {
	var inv NotifyInventory
	err := database.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE reject_reason = 'title_company'),
			COUNT(*) FILTER (WHERE reject_reason = 'description'),
			COUNT(*) FILTER (WHERE reject_reason = 'remote_lie'),
			COUNT(*) FILTER (WHERE reject_reason = 'duplicate'),
			COUNT(*) FILTER (WHERE reject_reason = 'detail_failed'),
			COUNT(*) FILTER (WHERE state = 'pending'),
			COUNT(*) FILTER (WHERE state = 'eligible'),
			COUNT(*) FILTER (WHERE state = 'notifying'),
			COUNT(*) FILTER (WHERE state = 'notified')
		FROM jobs
	`).Scan(
		&inv.Total,
		&inv.TitleCompany, &inv.Description, &inv.RemoteLie, &inv.Duplicate, &inv.DetailFailed,
		&inv.Pending, &inv.Eligible, &inv.Notifying, &inv.Notified,
	)
	return inv, err
}

// SetJobsState sets state for a claimed batch (notified after HTTP 200, eligible otherwise).
func SetJobsState(ctx context.Context, database *sql.DB, ids []int64, state string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := database.ExecContext(ctx, `
		UPDATE jobs SET
			state = $1,
			state_changed_at = CASE WHEN state IS DISTINCT FROM $1 THEN now() ELSE state_changed_at END,
			updated_at = now()
		WHERE id = ANY($2)
	`, state, ids)
	return err
}
