package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // register the "pgx" database/sql driver
)

// Connect opens a pool and pings with retry/backoff until the database is
// reachable, replacing the old separate wait_for_db.py script.
func Connect(ctx context.Context, dsn string) (*sql.DB, error) {
	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	pool.SetMaxOpenConns(20)
	pool.SetMaxIdleConns(10)
	pool.SetConnMaxLifetime(30 * time.Minute)

	const maxAttempts = 30
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = pool.PingContext(pingCtx)
		cancel()
		if err == nil {
			slog.Info("connected to postgres")
			return pool, nil
		}
		slog.Warn("waiting for postgres", "attempt", attempt, "max", maxAttempts, "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	pool.Close()
	return nil, fmt.Errorf("postgres unreachable after %d attempts: %w", maxAttempts, err)
}
