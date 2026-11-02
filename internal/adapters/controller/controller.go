// Package controller is the controller's loop: on each poll it starts every queued run, or agent task, it can claim,
// then follows the ones that run to their end; every sweep it cleans up after what ended otherwise. Any number of
// replicas may run it: each claims a run under a lease, and ending a run twice ends it once
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "A run's life").
package controller

import (
	"context"
	"log/slog"
	"time"
)

// maxStarts is the most a pass starts of one runner's queue before it follows what runs, so that a long queue never
// holds up what ended.
const maxStarts = 50

// Runner starts, follows and sweeps one kind of work in pods: runner.Service, jobs' runs, and taskrunner.Service,
// agents' tasks.
type Runner interface {
	Dispatch(ctx context.Context) (bool, error)
	Follow(ctx context.Context) error
	Sweep(ctx context.Context) error
}

// Run drives runners until ctx ends: a sweep first and then every sweep, and a pass every poll, each runner in turn. A
// failure is logged, and the next pass tries again.
func Run(ctx context.Context, poll, sweep time.Duration, runners ...Runner) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	var swept time.Time
	for {
		if time.Since(swept) >= sweep {
			for _, r := range runners {
				if err := r.Sweep(ctx); err != nil {
					slog.ErrorContext(ctx, "Sweeping the job namespaces failed", "error", err)
				}
			}
			swept = time.Now()
		}
		for _, r := range runners {
			pass(ctx, r)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// pass starts r's queue, up to maxStarts, until none waits or claiming fails, then follows what runs.
func pass(ctx context.Context, r Runner) {
	for range maxStarts {
		if ctx.Err() != nil {
			return
		}
		started, err := r.Dispatch(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "Starting a pod failed", "error", err)
		}
		if !started {
			break // none waits, or the database itself failed: wait for the next pass
		}
	}
	if err := r.Follow(ctx); err != nil {
		slog.ErrorContext(ctx, "Following the pods failed", "error", err)
	}
}
