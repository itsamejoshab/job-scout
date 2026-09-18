// Package pgtest starts an isolated Postgres for store integration tests.
//
// It uses TEST_DATABASE_URL when set (compose-backed). Otherwise it starts
// postgres:16-alpine with Docker. In-memory fakes cannot prove UNIQUE(job_url).
package pgtest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var (
	startOnce sync.Once
	startErr  error
	adminDSN  string
	cleanup   func()
	dbSeq     atomic.Uint64
)

// Open returns a connection to a fresh empty database. Callers apply migrations.
func Open(t *testing.T) *sql.DB {
	t.Helper()
	dsn := freshDSN(t)
	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pool.PingContext(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping postgres: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

func freshDSN(t *testing.T) string {
	t.Helper()
	startOnce.Do(func() {
		adminDSN, cleanup, startErr = startAdmin(t)
	})
	if startErr != nil {
		t.Fatalf("postgres test database is required for UNIQUE(job_url) tests: %v", startErr)
	}

	name := fmt.Sprintf("js_%d_%d", os.Getpid(), dbSeq.Add(1))
	admin, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatalf("open admin postgres: %v", err)
	}
	defer admin.Close()
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		drop, err := sql.Open("pgx", adminDSN)
		if err != nil {
			return
		}
		defer drop.Close()
		_, _ = drop.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
	})
	return rewriteDB(adminDSN, name)
}

// Run starts tests then stops a Docker Postgres started by Open.
// Put `os.Exit(pgtest.Run(m))` in TestMain of packages that call Open.
func Run(m *testing.M) int {
	code := m.Run()
	if cleanup != nil {
		cleanup()
	}
	return code
}

func startAdmin(t *testing.T) (string, func(), error) {
	t.Helper()
	if dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")); dsn != "" {
		if err := pingDSN(dsn); err != nil {
			return "", nil, fmt.Errorf("TEST_DATABASE_URL: %w", err)
		}
		return dsn, nil, nil
	}
	return startDockerPostgres()
}

func startDockerPostgres() (string, func(), error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return "", nil, fmt.Errorf("docker not found and TEST_DATABASE_URL is empty: %w", err)
	}
	name := fmt.Sprintf("jobscout-pgtest-%d", os.Getpid())
	_ = exec.Command("docker", "rm", "-f", name).Run()
	run := exec.Command("docker", "run", "-d", "--name", name,
		"-e", "POSTGRES_USER=jobscout",
		"-e", "POSTGRES_PASSWORD=jobscout",
		"-e", "POSTGRES_DB=postgres",
		"-p", "127.0.0.1::5432",
		"postgres:16-alpine",
	)
	out, err := run.CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("docker run postgres: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	stop := sync.OnceFunc(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
	})

	inspect, err := exec.Command("docker", "inspect", "--format",
		`{{(index (index .NetworkSettings.Ports "5432/tcp") 0).HostPort}}`, name).CombinedOutput()
	if err != nil {
		stop()
		return "", nil, fmt.Errorf("docker inspect port: %w (%s)", err, strings.TrimSpace(string(inspect)))
	}
	port := strings.TrimSpace(string(inspect))
	if port == "" {
		stop()
		return "", nil, fmt.Errorf("docker inspect port: empty host port")
	}
	dsn := fmt.Sprintf("postgres://jobscout:jobscout@127.0.0.1:%s/postgres?sslmode=disable", port)
	deadline := time.Now().Add(45 * time.Second)
	var pingErr error
	for time.Now().Before(deadline) {
		pingErr = pingDSN(dsn)
		if pingErr == nil {
			return dsn, stop, nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	stop()
	return "", nil, fmt.Errorf("postgres not ready: %w", pingErr)
}

func pingDSN(dsn string) error {
	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return pool.PingContext(ctx)
}

func rewriteDB(dsn, name string) string {
	if i := strings.Index(dsn, "?"); i >= 0 {
		base, query := dsn[:i], dsn[i:]
		slash := strings.LastIndex(base, "/")
		return base[:slash+1] + name + query
	}
	slash := strings.LastIndex(dsn, "/")
	if slash < 0 {
		return dsn
	}
	return dsn[:slash+1] + name
}
