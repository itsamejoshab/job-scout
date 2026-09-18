package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.temporal.io/sdk/client"
)

// Dial connects to the Temporal frontend, retrying until reachable. Replaces
// the Python connect_with_retry helper.
func Dial(ctx context.Context, hostPort string) (client.Client, error) {
	const maxAttempts = 30
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		c, err := client.Dial(client.Options{HostPort: hostPort})
		if err == nil {
			slog.Info("connected to temporal", "host", hostPort)
			return c, nil
		}
		lastErr = err
		slog.Warn("waiting for temporal", "attempt", attempt, "max", maxAttempts, "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("temporal unreachable after %d attempts: %w", maxAttempts, lastErr)
}
