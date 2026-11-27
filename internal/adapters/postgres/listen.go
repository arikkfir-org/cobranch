package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
)

// ListenPushes returns a channel that receives soon after the Git server records a push, until ctx ends: the push
// worker's wake. A connection of its own, outside pool, listens for the notification each push's transaction sends,
// and after losing its connection it takes another, waking the channel then too, since it may have missed some.
// Notifications that arrive while the channel is full make one wake.
func ListenPushes(ctx context.Context, pool *pgxpool.Pool) <-chan struct{} {
	wake := make(chan struct{}, 1)
	go listen(ctx, pool.Config().ConnConfig, "pushes", (*queries.Queries).ListenPushes,
		func() { notify(wake) }, func(*pgconn.Notification) { notify(wake) })
	return wake
}

// Hub tells a server's followers when what they follow may have changed (chats.Notifier, orgs.Notifier, live.Notifier):
// a connection of its own listens for the notifications each change's transaction sends, naming an ID (a chat's
// subject; a principal or an organization), and wakes those who watch that ID. After losing its connection it takes another, waking every
// watcher then, since they may have missed some.
type Hub struct {
	mu       sync.Mutex
	watchers map[uuid.UUID]map[chan struct{}]struct{}
}

// ListenChats returns the hub of the chats' followers, listening outside pool until ctx ends.
func ListenChats(ctx context.Context, pool *pgxpool.Pool) *Hub {
	return listenHub(ctx, pool, "chats", (*queries.Queries).ListenChats)
}

// ListenOrganizations returns the hub of the followers of people's organizations, listening outside pool until ctx
// ends: a change wakes those who watch the principal or the organization it names.
func ListenOrganizations(ctx context.Context, pool *pgxpool.Pool) *Hub {
	return listenHub(ctx, pool, "organizations", (*queries.Queries).ListenOrganizations)
}

// ListenChanges returns the hub of the followers of the Console's live pages, listening outside pool until ctx ends: a
// change wakes those who watch what it names, such as its organization.
func ListenChanges(ctx context.Context, pool *pgxpool.Pool) *Hub {
	return listenHub(ctx, pool, "changes", (*queries.Queries).ListenChanges)
}

// listenHub returns a hub that start's notifications wake, listening outside pool until ctx ends.
func listenHub(ctx context.Context, pool *pgxpool.Pool, what string, start func(*queries.Queries, context.Context) error) *Hub {
	h := &Hub{watchers: map[uuid.UUID]map[chan struct{}]struct{}{}}
	go listen(ctx, pool.Config().ConnConfig, what, start, h.wakeAll, h.wake)
	return h
}

// Watch returns a channel that receives soon after a change that names one of ids commits, and a function that stops
// it. Changes that come while the channel is full make one receive.
func (h *Hub) Watch(ids ...uuid.UUID) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range ids {
		if h.watchers[id] == nil {
			h.watchers[id] = map[chan struct{}]struct{}{}
		}
		h.watchers[id][ch] = struct{}{}
	}
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, id := range ids {
			delete(h.watchers[id], ch)
			if len(h.watchers[id]) == 0 {
				delete(h.watchers, id)
			}
		}
	}
}

// wake wakes those who watch the ID a notification names.
func (h *Hub) wake(n *pgconn.Notification) {
	id, err := uuid.Parse(n.Payload)
	if err != nil {
		return // no ID of ours
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.watchers[id] {
		notify(ch)
	}
}

// wakeAll wakes every watcher.
func (h *Hub) wakeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, chs := range h.watchers {
		for ch := range chs {
			notify(ch)
		}
	}
}

// listen listens with start, on a connection of its own, until ctx ends, calling listening each time it starts to
// listen and heard for each notification. It connects again a second after a failure, then longer, at most every 30
// seconds, and again a second after once a connection listened.
func listen(ctx context.Context, cfg *pgx.ConnConfig, what string, start func(*queries.Queries, context.Context) error, listening func(), heard func(*pgconn.Notification)) {
	backoff := time.Second
	for {
		listened, err := listenOnce(ctx, cfg, start, listening, heard)
		if ctx.Err() != nil {
			return
		}
		if listened {
			backoff = time.Second
		}
		slog.WarnContext(ctx, "Listening for "+what+" failed; listening again", "error", err, "after", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(2*backoff, 30*time.Second)
	}
}

// listenOnce listens on one connection until it fails or ctx ends, and reports whether it got to listen.
func listenOnce(ctx context.Context, cfg *pgx.ConnConfig, start func(*queries.Queries, context.Context) error, listening func(), heard func(*pgconn.Notification)) (bool, error) {
	conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		return false, fmt.Errorf("connecting: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }() // a failed connection has nothing left to close
	if err := start(queries.New(conn), ctx); err != nil {
		return false, fmt.Errorf("listening: %w", err)
	}
	listening() // what changed while nothing listened
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return true, fmt.Errorf("waiting for a notification: %w", err)
		}
		heard(n)
	}
}

// notify wakes the channel, unless a wake already waits there.
func notify(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}
