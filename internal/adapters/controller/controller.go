// Package controller is the controller's loop: on each poll it starts every queued run, or agent task, it can claim,
// then follows the ones that run to their end; every minute it queues the schedules' runs, and every sweep it cleans up
// after what ended otherwise. Any number of replicas may run it: each claims a run under a lease, and ending a run
// twice ends it once
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

// scheduleEvery is how often the loop queues the schedules' runs: a schedule fires on whole minutes.
const scheduleEvery = time.Minute

// Runner starts, follows and sweeps one kind of work in pods: runner.Service, jobs' runs, and taskrunner.Service,
// agents' tasks.
type Runner interface {
	Dispatch(ctx context.Context) (bool, error)
	Follow(ctx context.Context) error
	Sweep(ctx context.Context) error
}

// Scheduler is a Runner that also queues work on schedules, every minute: runner.Service, which queues the schedules'
// runs.
type Scheduler interface {
	Schedule(ctx context.Context) error
}

// Run drives runners until ctx ends: a sweep first and then every sweep, the schedules of those that are Schedulers
// every minute, and a pass every poll, each runner in turn. A failure is logged, and the next pass tries again.
func Run(ctx context.Context, poll, sweep time.Duration, runners ...Runner) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	var swept, scheduled time.Time
	for {
		if time.Since(swept) >= sweep {
			for _, r := range runners {
				if err := r.Sweep(ctx); err != nil {
					slog.ErrorContext(ctx, "Sweeping the job namespaces failed", "error", err)
				}
			}
			swept = time.Now()
		}
		if time.Since(scheduled) >= scheduleEvery {
			schedule(ctx, runners)
			scheduled = time.Now()
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

// schedule queues the schedules' work of those of runners that are Schedulers.
func schedule(ctx context.Context, runners []Runner) {
	for _, r := range runners {
		if s, ok := r.(Scheduler); ok {
			if err := s.Schedule(ctx); err != nil {
				slog.ErrorContext(ctx, "Queueing the schedules' runs failed", "error", err)
			}
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
