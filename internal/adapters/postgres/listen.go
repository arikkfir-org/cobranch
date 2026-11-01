package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
)

// ListenPushes returns a channel that receives soon after the Git server records a push, until ctx ends: the push
// worker's wake. A connection of its own, outside pool, listens for the notification each push's transaction sends,
// and after losing its connection it takes another, waking the channel then too, since it may have missed some.
// Notifications that arrive while the channel is full make one wake.
func ListenPushes(ctx context.Context, pool *pgxpool.Pool) <-chan struct{} {
	wake := make(chan struct{}, 1)
	go listenPushes(ctx, pool.Config().ConnConfig, wake)
	return wake
}

// listenPushes listens until ctx ends, connecting again a second after a failure, then longer, at most every 30
// seconds, and again a second after once a connection listened.
func listenPushes(ctx context.Context, cfg *pgx.ConnConfig, wake chan<- struct{}) {
	backoff := time.Second
	for {
		listened, err := listenOnce(ctx, cfg, wake)
		if ctx.Err() != nil {
			return
		}
		if listened {
			backoff = time.Second
		}
		slog.WarnContext(ctx, "Listening for pushes failed; listening again", "error", err, "after", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(2*backoff, 30*time.Second)
	}
}

// listenOnce listens on one connection until it fails or ctx ends, and reports whether it got to listen.
func listenOnce(ctx context.Context, cfg *pgx.ConnConfig, wake chan<- struct{}) (bool, error) {
	conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		return false, fmt.Errorf("connecting: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }() // a failed connection has nothing left to close
	if err := queries.New(conn).ListenPushes(ctx); err != nil {
		return false, fmt.Errorf("listening: %w", err)
	}
	notify(wake) // pushes recorded while nothing listened
	for {
		if _, err := conn.WaitForNotification(ctx); err != nil {
			return true, fmt.Errorf("waiting for a notification: %w", err)
		}
		notify(wake)
	}
}

// notify wakes the channel, unless a wake already waits there.
func notify(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}
