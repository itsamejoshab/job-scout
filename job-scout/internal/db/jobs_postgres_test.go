package db

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/db/pgtest"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Run(m))
}

func TestMigrate_BackfillsPendingAndDropsExactURLDuplicates(t *testing.T) {
	pool := pgtest.Open(t)
	applyNamedMigration(t, pool, "0001_init.sql")

	url := "https://www.linkedin.com/jobs/view/dup-keep/"
	if _, err := pool.Exec(`INSERT INTO jobs (title, company, location, job_url) VALUES ('t','c','l',$1)`, url); err != nil {
		t.Fatalf("insert first duplicate: %v", err)
	}
	if _, err := pool.Exec(`INSERT INTO jobs (title, company, location, job_url) VALUES ('t','c','l',$1)`, url); err != nil {
		t.Fatalf("insert second duplicate: %v", err)
	}
	if _, err := pool.Exec(`INSERT INTO jobs (title, company, location, job_url, notified) VALUES ('t','c','l',$1, TRUE)`, "https://www.linkedin.com/jobs/view/other/"); err != nil {
		t.Fatalf("insert other url: %v", err)
	}

	var keepID int64
	if err := pool.QueryRow(`SELECT MIN(id) FROM jobs WHERE job_url = $1`, url).Scan(&keepID); err != nil {
		t.Fatalf("min id: %v", err)
	}

	if err := Migrate(pool); err != nil {
		t.Fatalf("migrate after pre-existing duplicates: %v", err)
	}

	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM jobs WHERE job_url = $1`, url).Scan(&n); err != nil {
		t.Fatalf("count dup url: %v", err)
	}
	if n != 1 {
		t.Errorf("exact-URL duplicates after migrate = %d, want 1 (keep lowest id, drop extras)", n)
	}
	var gotID int64
	if err := pool.QueryRow(`SELECT id FROM jobs WHERE job_url = $1`, url).Scan(&gotID); err != nil {
		t.Fatalf("kept id: %v", err)
	}
	if gotID != keepID {
		t.Errorf("kept id = %d, want lowest id %d", gotID, keepID)
	}

	var other int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM jobs WHERE job_url = $1`, "https://www.linkedin.com/jobs/view/other/").Scan(&other); err != nil {
		t.Fatalf("count other url: %v", err)
	}
	if other != 1 {
		t.Errorf("distinct URL rows = %d, want 1 (cleanup must not drop different URLs)", other)
	}

	var pending int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM jobs WHERE state = 'pending'`).Scan(&pending); err != nil {
		t.Errorf("existing rows must backfill state=pending, select err: %v", err)
	} else if pending != 2 {
		t.Errorf("pending backfill count = %d, want 2", pending)
	}
	var otherState string
	if err := pool.QueryRow(`SELECT state FROM jobs WHERE job_url = $1`, "https://www.linkedin.com/jobs/view/other/").Scan(&otherState); err != nil {
		t.Errorf("legacy notified=true row must backfill pending: %v", err)
	} else if otherState != "pending" {
		t.Errorf("legacy notified=true row state = %q, want pending", otherState)
	}
}

func TestJobs_SchemaIdentityStateAndRemoteColumns(t *testing.T) {
	pool := migratedPool(t)

	dataType, maxLen := jobURLColumn(t, pool)
	wideEnough := dataType == "text" || (dataType == "character varying" && (maxLen == 0 || maxLen >= 2048))
	if !wideEnough {
		t.Errorf("job_url type=%q max_len=%d, want TEXT or VARCHAR >= 2048", dataType, maxLen)
	}
	if !hasUniqueJobURL(t, pool) {
		t.Error("jobs.job_url must have a UNIQUE constraint so concurrent inserts cannot duplicate")
	}
	if !hasColumn(t, pool, "state") {
		t.Error("jobs.state column is required")
	}
	if !hasColumn(t, pool, "reject_reason") {
		t.Error("jobs.reject_reason column is required")
	}
	if !hasColumn(t, pool, "is_remote") {
		t.Error("jobs.is_remote column is required")
	}
	if !hasColumn(t, pool, "search_intention") {
		t.Error("jobs.search_intention column is required")
	}
	if !hasColumn(t, pool, "detail_attempts") {
		t.Error("jobs.detail_attempts column is required")
	}
	if !hasColumn(t, pool, "state_changed_at") {
		t.Error("jobs.state_changed_at column is required")
	}
	if !stateCheckExists(t, pool) {
		t.Error("jobs.state must be CHECK-constrained to pending, rejected, needs_detail, ready, applied, dismissed")
	}
}

func TestJobs_BareInsertUsesRequiredDefaults(t *testing.T) {
	pool := migratedPool(t)
	if !hasColumn(t, pool, "state") || !hasColumn(t, pool, "is_remote") || !hasColumn(t, pool, "detail_attempts") || !hasColumn(t, pool, "state_changed_at") {
		t.Error("state, is_remote, detail_attempts, and state_changed_at must exist with database defaults")
		return
	}
	url := "https://www.linkedin.com/jobs/view/bare-defaults/"
	if _, err := pool.Exec(`INSERT INTO jobs (title, company, location, job_url) VALUES ('t','c','l',$1)`, url); err != nil {
		t.Fatalf("insert omitting state columns: %v", err)
	}
	var (
		state        string
		reject       sql.NullString
		remote       bool
		attempts     int
		stateChanged time.Time
	)
	if err := pool.QueryRow(`SELECT state, reject_reason, is_remote, detail_attempts, state_changed_at FROM jobs WHERE job_url = $1`, url).
		Scan(&state, &reject, &remote, &attempts, &stateChanged); err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if state != "pending" {
		t.Errorf("default state = %q, want pending", state)
	}
	if reject.Valid {
		t.Errorf("default reject_reason = %q, want NULL", reject.String)
	}
	if remote {
		t.Error("default is_remote = true, want false")
	}
	if attempts != 0 {
		t.Errorf("default detail_attempts = %d, want 0", attempts)
	}
	if stateChanged.IsZero() {
		t.Error("default state_changed_at must be set")
	}
	if _, err := pool.Exec(`INSERT INTO jobs (title, company, location, job_url, state) VALUES ('t','c','l','https://www.linkedin.com/jobs/view/null-state/', NULL)`); err == nil {
		t.Error("state must be NOT NULL")
	}
}

func TestInsertJobIfNew_TrimsURLDoesNotTruncateAndDefaultsState(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	base := "https://www.linkedin.com/jobs/view/"
	long := base + strings.Repeat("x", 2048-len(base)-1) + "/"
	if len(long) != 2048 {
		t.Fatalf("test URL length=%d, want 2048", len(long))
	}
	padded := "  " + long + "  "

	inserted, err := InsertJobIfNew(ctx, pool, sampleJob(padded))
	if err != nil {
		t.Errorf("insert must accept a trimmed job_url of length 2048: %v", err)
		return
	}
	if !inserted {
		t.Fatal("first insert of a new URL must return true")
	}

	var stored string
	if err := pool.QueryRow(`SELECT job_url FROM jobs`).Scan(&stored); err != nil {
		t.Fatalf("load stored job_url: %v", err)
	}
	if stored != long {
		t.Errorf("stored job_url length=%d value=%q, want trimmed full URL length=%d (no 250-character cut)", len(stored), stored, len(long))
	}

	var (
		state        string
		reject       sql.NullString
		remote       bool
		attempts     int
		stateChanged time.Time
	)
	err = pool.QueryRow(`
		SELECT state, reject_reason, is_remote, detail_attempts, state_changed_at
		FROM jobs
	`).Scan(&state, &reject, &remote, &attempts, &stateChanged)
	if err != nil {
		t.Errorf("new job must persist state, reject_reason, is_remote, detail_attempts, state_changed_at: %v", err)
	} else {
		if state != "pending" {
			t.Errorf("new job state = %q, want pending", state)
		}
		if reject.Valid {
			t.Errorf("reject_reason = %q, want NULL", reject.String)
		}
		if remote {
			t.Error("is_remote default = true, want false")
		}
		if attempts != 0 {
			t.Errorf("detail_attempts = %d, want 0", attempts)
		}
		if stateChanged.IsZero() {
			t.Error("state_changed_at default must be set")
		}
	}

	again, err := InsertJobIfNew(ctx, pool, sampleJob(" "+long+" "))
	if err != nil {
		t.Fatalf("second insert of trimmed URL: %v", err)
	}
	if again {
		t.Error("second insert of the same trimmed URL must return false")
	}
	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM jobs WHERE job_url = $1`, long).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("rows for trimmed URL = %d, want 1", n)
	}
}

func TestInsertJobIfNew_RemoteORLeavesStateUnchanged(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	url := "https://www.linkedin.com/jobs/view/remote-or/"

	first := sampleJob(url)
	first.IsRemote = false
	if _, err := InsertJobIfNew(ctx, pool, first); err != nil {
		t.Fatalf("insert onsite: %v", err)
	}

	if _, err := pool.Exec(`UPDATE jobs SET state = 'applied', notified_at = now() WHERE job_url = $1`, url); err != nil {
		t.Errorf("must persist state so a second sighting can leave it unchanged: %v", err)
	}

	second := sampleJob(url)
	second.Title = "Changed title"
	second.IsRemote = true
	inserted, err := InsertJobIfNew(ctx, pool, second)
	if err != nil {
		t.Fatalf("second sighting remote: %v", err)
	}
	if inserted {
		t.Error("second sighting must not insert another row")
	}

	third := sampleJob(url)
	third.IsRemote = false
	if _, err := InsertJobIfNew(ctx, pool, third); err != nil {
		t.Fatalf("third sighting onsite: %v", err)
	}

	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM jobs WHERE job_url = $1`, url).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}

	var remote bool
	if err := pool.QueryRow(`SELECT is_remote FROM jobs WHERE job_url = $1`, url).Scan(&remote); err != nil {
		t.Errorf("is_remote must be stored so remote search can OR onto the row: %v", err)
	} else if !remote {
		t.Error("is_remote must become true when any scrape of the URL is remote (OR)")
	}

	var title string
	if err := pool.QueryRow(`SELECT title FROM jobs WHERE job_url = $1`, url).Scan(&title); err != nil {
		t.Fatalf("title: %v", err)
	}
	if title != first.Title {
		t.Errorf("second sighting changed title to %q, want original %q", title, first.Title)
	}

	var state string
	if err := pool.QueryRow(`SELECT state FROM jobs WHERE job_url = $1`, url).Scan(&state); err != nil {
		t.Errorf("state must persist so a second sighting cannot reset it: %v", err)
	} else if state != "applied" {
		t.Errorf("second sighting changed state to %q, want applied", state)
	}
}

func TestInsertJobIfNew_ConcurrentSameURLDoesNotDuplicate(t *testing.T) {
	pool := migratedPool(t)
	if !hasUniqueJobURL(t, pool) {
		t.Error("jobs.job_url must have a UNIQUE constraint so concurrent inserts cannot duplicate")
	}
	ctx := context.Background()
	url := "https://www.linkedin.com/jobs/view/concurrent/"
	const workers = 24

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(remote bool) {
			defer wg.Done()
			<-start
			j := sampleJob(url)
			j.IsRemote = remote
			_, err := InsertJobIfNew(ctx, pool, j)
			if err != nil {
				errs <- err
			}
		}(i%2 == 0)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent insert error: %v", err)
	}

	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM jobs WHERE job_url = $1`, url).Scan(&n); err != nil {
		t.Fatalf("count concurrent url: %v", err)
	}
	if n != 1 {
		t.Errorf("concurrent inserts created %d rows for the same URL, want 1", n)
	}
	var remote bool
	if err := pool.QueryRow(`SELECT is_remote FROM jobs WHERE job_url = $1`, url).Scan(&remote); err != nil {
		t.Errorf("concurrent remote OR must persist is_remote: %v", err)
	} else if !remote {
		t.Error("concurrent inserts must OR is_remote true when any worker saw a remote search")
	}
}

func TestInsertJobIfNew_StateCheckRejectsInvalid(t *testing.T) {
	pool := migratedPool(t)
	if _, err := InsertJobIfNew(context.Background(), pool, sampleJob("https://www.linkedin.com/jobs/view/check/")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if !hasColumn(t, pool, "state") {
		t.Error("jobs.state column is required")
		return
	}
	_, err := pool.Exec(`UPDATE jobs SET state = 'bogus'`)
	if err == nil {
		t.Error("state CHECK must reject values outside pending, rejected, needs_detail, ready, applied, dismissed")
	}
}

func TestGetJobStats_NewJobsIsPendingCountByState(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/stats-a/")); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/stats-b/")); err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET new = FALSE`); err != nil {
		t.Fatalf("clear legacy new flag: %v", err)
	}

	stats, err := GetJobStats(ctx, pool)
	if err != nil {
		t.Fatalf("GetJobStats: %v", err)
	}
	if stats.TotalJobs != 2 {
		t.Errorf("total_jobs = %d, want 2", stats.TotalJobs)
	}
	if stats.NewJobs != 2 {
		t.Errorf("new_jobs = %d, want 2 (new_jobs is the pending count, not the legacy new boolean)", stats.NewJobs)
	}

	if _, err := pool.Exec(`UPDATE jobs SET state = 'rejected' WHERE job_url LIKE '%stats-b/'`); err != nil {
		t.Errorf("must persist state to count by state: %v", err)
		return
	}

	stats, err = GetJobStats(ctx, pool)
	if err != nil {
		t.Fatalf("GetJobStats after reject: %v", err)
	}
	if stats.NewJobs != 1 {
		t.Errorf("new_jobs after one reject = %d, want 1", stats.NewJobs)
	}
	if stats.ByState["pending"] != 1 {
		t.Errorf("by_state pending = %d, want 1", stats.ByState["pending"])
	}
	if stats.ByState["rejected"] != 1 {
		t.Errorf("by_state rejected = %d, want 1", stats.ByState["rejected"])
	}
	for _, st := range []string{"pending", "rejected", "needs_detail", "ready", "applied", "dismissed"} {
		if _, ok := stats.ByState[st]; !ok {
			t.Errorf("by_state missing key %q", st)
		}
	}
	for _, st := range []string{"needs_detail", "ready", "applied", "dismissed"} {
		if stats.ByState[st] != 0 {
			t.Errorf("by_state %s = %d, want 0", st, stats.ByState[st])
		}
	}
}

func TestListJobs_IncludesState(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	if _, err := InsertJobIfNew(ctx, pool, sampleJob("https://www.linkedin.com/jobs/view/list-state/")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	jobs, err := ListJobs(ctx, pool, 10, 0)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("ListJobs len=%d, want 1", len(jobs))
	}
	if jobs[0].State != "pending" {
		t.Errorf("ListJobs state = %q, want pending", jobs[0].State)
	}
}

func migratedPool(t *testing.T) *sql.DB {
	t.Helper()
	pool := pgtest.Open(t)
	if err := Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

func applyNamedMigration(t *testing.T, pool *sql.DB, name string) {
	t.Helper()
	if _, err := pool.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMP DEFAULT now()
	)`); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	body, err := migrationFS.ReadFile("migrations/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if _, err := pool.Exec(string(body)); err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
	if _, err := pool.Exec(`INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
		t.Fatalf("record %s: %v", name, err)
	}
}

func TestInsertJobIfNew_OnsiteIntentionWinsOverRemote(t *testing.T) {
	pool := migratedPool(t)
	ctx := context.Background()
	url := "https://www.dice.com/job-detail/intention-win"

	first := sampleJob(url)
	first.JobSource = SourceDice
	first.SearchIntention = SearchIntentionRemote
	if _, err := InsertJobIfNew(ctx, pool, first); err != nil {
		t.Fatalf("insert remote: %v", err)
	}
	var intention string
	if err := pool.QueryRow(`SELECT search_intention FROM jobs WHERE job_url = $1`, url).Scan(&intention); err != nil {
		t.Fatalf("select intention: %v", err)
	}
	if intention != SearchIntentionRemote {
		t.Fatalf("first stamp = %q, want remote", intention)
	}

	second := sampleJob(url)
	second.JobSource = SourceDice
	second.SearchIntention = SearchIntentionOnsite
	if _, err := InsertJobIfNew(ctx, pool, second); err != nil {
		t.Fatalf("insert onsite: %v", err)
	}
	if err := pool.QueryRow(`SELECT search_intention FROM jobs WHERE job_url = $1`, url).Scan(&intention); err != nil {
		t.Fatalf("select after onsite: %v", err)
	}
	if intention != SearchIntentionOnsite {
		t.Errorf("onsite must overwrite remote, got %q", intention)
	}

	third := sampleJob(url)
	third.JobSource = SourceDice
	third.SearchIntention = SearchIntentionRemote
	if _, err := InsertJobIfNew(ctx, pool, third); err != nil {
		t.Fatalf("insert remote again: %v", err)
	}
	if err := pool.QueryRow(`SELECT search_intention FROM jobs WHERE job_url = $1`, url).Scan(&intention); err != nil {
		t.Fatalf("select after remote again: %v", err)
	}
	if intention != SearchIntentionOnsite {
		t.Errorf("later remote must not overwrite onsite, got %q", intention)
	}
}

func sampleJob(url string) Job {
	return Job{
		JobSource:       SourceLinkedIn,
		Title:           "IT Help Desk",
		Company:         "Acme",
		Location:        "Remote",
		JobURL:          url,
		SearchIntention: SearchIntentionOnsite,
	}
}

func jobURLColumn(t *testing.T, pool *sql.DB) (string, int) {
	t.Helper()
	var dataType string
	var maxLen sql.NullInt64
	err := pool.QueryRow(`
		SELECT data_type, character_maximum_length
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'jobs' AND column_name = 'job_url'
	`).Scan(&dataType, &maxLen)
	if err != nil {
		t.Fatalf("job_url column metadata: %v", err)
	}
	if maxLen.Valid {
		return dataType, int(maxLen.Int64)
	}
	return dataType, 0
}

func hasUniqueJobURL(t *testing.T, pool *sql.DB) bool {
	t.Helper()
	var n int
	err := pool.QueryRow(`
		SELECT COUNT(*)
		FROM pg_indexes
		WHERE schemaname = 'public' AND tablename = 'jobs'
		  AND indexdef ILIKE '%UNIQUE%'
		  AND indexdef ~* '\(job_url\)'
	`).Scan(&n)
	if err != nil {
		t.Fatalf("unique job_url lookup: %v", err)
	}
	return n > 0
}

func hasColumn(t *testing.T, pool *sql.DB, name string) bool {
	t.Helper()
	var n int
	err := pool.QueryRow(`
		SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'jobs' AND column_name = $1
	`, name).Scan(&n)
	if err != nil {
		t.Fatalf("column %s lookup: %v", name, err)
	}
	return n > 0
}

func stateCheckExists(t *testing.T, pool *sql.DB) bool {
	t.Helper()
	var n int
	err := pool.QueryRow(`
		SELECT COUNT(*)
		FROM pg_constraint
		WHERE conrelid = 'jobs'::regclass AND contype = 'c'
		  AND pg_get_constraintdef(oid) ILIKE '%pending%'
		  AND pg_get_constraintdef(oid) ILIKE '%rejected%'
		  AND pg_get_constraintdef(oid) ILIKE '%ready%'
		  AND pg_get_constraintdef(oid) ILIKE '%applied%'
		  AND pg_get_constraintdef(oid) ILIKE '%dismissed%'
	`).Scan(&n)
	if err != nil {
		t.Fatalf("state check lookup: %v", err)
	}
	return n > 0
}
