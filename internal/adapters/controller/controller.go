// Package controller is the controller's loop, which it runs for its runs and for its deliveries alike: on each poll it
// starts every queued one it can claim, then follows those that work to their end; every sweep it cleans up after what
// ended otherwise. Any number of replicas may run it: each claims under a lease, and ending one twice ends it once
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "A run's life", and
// https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md, "A delivery's life").
package controller

import (
	"context"
	"log/slog"
	"time"
)

// maxStarts is the most a pass starts before it follows those that work, so that a long queue never holds up those
// that ended.
const maxStarts = 50

// Runner starts, follows and sweeps the runs, or the deliveries: runner.Service, deliverer.Service.
type Runner interface {
	Dispatch(ctx context.Context) (bool, error)
	Follow(ctx context.Context) error
	Sweep(ctx context.Context) error
}

// Run drives r, which works through what, until ctx ends: a sweep first and then every sweep, and a pass every poll. A
// failure is logged, naming what, and the next pass tries again.
func Run(ctx context.Context, what string, r Runner, poll, sweep time.Duration) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	var swept time.Time
	for {
		if time.Since(swept) >= sweep {
			if err := r.Sweep(ctx); err != nil {
				slog.ErrorContext(ctx, "Sweeping failed", "work", what, "error", err)
			}
			swept = time.Now()
		}
		pass(ctx, what, r)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// pass starts the queued ones, up to maxStarts, until none waits or claiming fails, then follows those that work.
func pass(ctx context.Context, what string, r Runner) {
	for range maxStarts {
		if ctx.Err() != nil {
			return
		}
		started, err := r.Dispatch(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "Starting failed", "work", what, "error", err)
		}
		if !started {
			break // none waits, or the database itself failed: wait for the next pass
		}
	}
	if err := r.Follow(ctx); err != nil {
		slog.ErrorContext(ctx, "Following failed", "work", what, "error", err)
	}
}
