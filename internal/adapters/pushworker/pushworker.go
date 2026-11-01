// Package pushworker is the push worker's loop: it processes the pushes the Git server recorded, one after another
// until none waits, then sleeps until a notification wakes it, or for its poll interval in case it missed one. A push
// it processes is already committed, and its client gone: nothing waits on the push worker, which may run any number
// of replicas, none while no push waits
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/git-hosting.md, "A push").
package pushworker

import (
	"context"
	"log/slog"
	"time"
)

// Processor processes the push that has waited longest, and reports whether one waited: git.Service.
type Processor interface {
	Process(ctx context.Context) (bool, error)
}

// Run processes pushes until ctx ends: every waiting push, then again on each wake, or every poll. A push it fails
// waits for another try, as the Processor keeps it, which the first wake or poll once it is due takes; Run logs the
// failure and goes on with the rest.
func Run(ctx context.Context, p Processor, wake <-chan struct{}, poll time.Duration) {
	timer := time.NewTimer(poll)
	defer timer.Stop()
	for {
		drain(ctx, p)
		timer.Reset(poll)
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-timer.C:
		}
	}
}

// drain processes pushes until none waits, ctx ends, or the database fails.
func drain(ctx context.Context, p Processor) {
	for ctx.Err() == nil {
		processed, err := p.Process(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "Processing a push failed", "error", err)
			if !processed {
				return // nothing claimed: the database itself failed, so wait before asking again
			}
			continue
		}
		if !processed {
			return
		}
	}
}
