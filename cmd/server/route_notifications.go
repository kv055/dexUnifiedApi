package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

const snapshotRoutingChannel = "snapshot_routing_changed"

// listenSnapshotRoutingChanges keeps one dedicated PostgreSQL connection for
// invalidation events. Each event reloads the complete routing context and
// atomically swaps a rebuilt book. The periodic refresh remains a recovery path
// for transient listener failures.
func listenSnapshotRoutingChanges(ctx context.Context, connectionString string) {
	for ctx.Err() == nil {
		conn, err := pgx.Connect(ctx, connectionString)
		if err != nil {
			slog.ErrorContext(ctx, "connect snapshot routing listener", "err", err)
			if !waitRouteListenerRetry(ctx) {
				return
			}
			continue
		}
		if _, err = conn.Exec(ctx, "LISTEN "+snapshotRoutingChannel); err != nil {
			_ = conn.Close(ctx)
			slog.ErrorContext(ctx, "listen for snapshot routing changes", "err", err)
			if !waitRouteListenerRetry(ctx) {
				return
			}
			continue
		}
		for ctx.Err() == nil {
			notification, waitErr := conn.WaitForNotification(ctx)
			if waitErr != nil {
				break
			}
			if err := refreshSnapshotRoutingContext(ctx); err != nil {
				slog.ErrorContext(ctx, "rebuild snapshot route book after database notification", "source", notification.Payload, "err", err)
			} else {
				slog.InfoContext(ctx, "rebuilt snapshot route book after database notification", "source", notification.Payload)
			}
		}
		_ = conn.Close(context.Background())
		if !waitRouteListenerRetry(ctx) {
			return
		}
	}
}

func waitRouteListenerRetry(ctx context.Context) bool {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
