// Package live follows what a read of the domain answers as it changes, for the clients that show it live: a service
// reads again whenever its Notifier says what the read covers may have changed, and once a minute for a change it
// missed, and sends each answer that differs from the one before.
package live

import (
	"context"
	"log/slog"
	"reflect"
	"time"

	"github.com/google/uuid"
)

// Notifier tells the servers that follow what a read answers when it may have changed.
type Notifier interface {
	// Watch returns a channel that receives soon after a change commits that names one of ids, and a function that
	// stops it. Changes that come while the channel is full make one receive.
	Watch(ids ...uuid.UUID) (<-chan struct{}, func())
}

// Poll is how often a follower reads again without a wake, for a change it missed.
const Poll = time.Minute

// Follow sends what read answers at once, then again each time it differs from what it sent last, until ctx ends or a
// read fails, and then closes the channel. It reads again whenever n says a change named one of ids, and every poll
// for a change it missed; a nil n never wakes it. It watches before the first read, so no change after that read goes
// unseen, and answers the first read's error itself.
func Follow[T any](ctx context.Context, n Notifier, poll time.Duration, read func(context.Context) (T, error), ids ...uuid.UUID) (<-chan T, error) {
	var wake <-chan struct{}
	stop := func() {}
	if n != nil {
		wake, stop = n.Watch(ids...)
	}
	first, err := read(ctx)
	if err != nil {
		stop()
		return nil, err
	}
	out := make(chan T, 1)
	out <- first
	go func() {
		defer close(out)
		defer stop()
		follow(ctx, wake, poll, read, first, out)
	}()
	return out, nil
}

// follow sends to out what read answers each time wake receives or poll passes, while it differs from last, until ctx
// ends or a read fails, which it logs unless ctx ended.
func follow[T any](ctx context.Context, wake <-chan struct{}, poll time.Duration, read func(context.Context) (T, error), last T, out chan<- T) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-ticker.C:
		}
		next, err := read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				slog.ErrorContext(ctx, "Following a read failed", "error", err)
			}
			return
		}
		if reflect.DeepEqual(next, last) {
			continue
		}
		select {
		case out <- next:
			last = next
		case <-ctx.Done():
			return
		}
	}
}
