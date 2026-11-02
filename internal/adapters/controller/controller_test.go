package controller_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"cobranch.dev/internal/adapters/controller"
)

// runner answers Dispatch from its queue, a step each call, and counts its calls.
type runner struct {
	mu                                     sync.Mutex
	queue                                  []error
	dispatched, followed, swept, scheduled int
	// stop, when set, ends Run once it has followed the runs.
	stop context.CancelFunc
}

// claimed is a step that claimed a run; errClaim one that failed to claim any.
var errClaim = errors.New("database down")

func (r *runner) Dispatch(context.Context) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dispatched++
	if len(r.queue) == 0 {
		return false, nil
	}
	err := r.queue[0]
	r.queue = r.queue[1:]
	return !errors.Is(err, errClaim), err
}

func (r *runner) Follow(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.followed++
	if r.stop != nil {
		r.stop()
	}
	return errors.New("one organization's pods couldn't be listed")
}

func (r *runner) Sweep(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.swept++
	return nil
}

func (r *runner) Schedule(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scheduled++
	return errors.New("one repository's settings don't decode")
}

// unscheduled is a Runner that queues nothing on schedules, as taskrunner.Service.
type unscheduled struct{ controller.Runner }

func TestRun(t *testing.T) {
	tests := []struct {
		name  string
		queue []error
		// wantFirst is how many Dispatch calls the first pass makes.
		wantFirst int
	}{
		{name: "drains the queue", queue: []error{nil, nil, nil}, wantFirst: 4},
		{name: "a run that failed to start doesn't stop the rest", queue: []error{errors.New("no pod"), nil}, wantFirst: 3},
		{name: "a database that fails waits for the next pass", queue: []error{errClaim, nil}, wantFirst: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			r := &runner{queue: tt.queue, stop: cancel}
			controller.Run(ctx, time.Hour, time.Hour, r)
			if r.dispatched != tt.wantFirst || r.followed != 1 || r.swept != 1 || r.scheduled != 1 {
				t.Errorf("one pass dispatched %d, followed %d, swept %d, scheduled %d, want %d, 1, 1, 1", r.dispatched, r.followed, r.swept,
					r.scheduled, tt.wantFirst)
			}
		})
	}
}

func TestRunPollsAndSweeps(t *testing.T) {
	r := &runner{}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	controller.Run(ctx, 10*time.Millisecond, 50*time.Millisecond, r)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.followed < 5 || r.swept < 2 || r.swept >= r.followed || r.scheduled != 1 {
		t.Errorf("in 200ms, followed %d times, swept %d and scheduled %d, want every 10ms, every 50ms and once a minute", r.followed,
			r.swept, r.scheduled)
	}
}

func TestRunEveryRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runs, tasks := &runner{queue: []error{nil}}, &runner{queue: []error{nil, nil}, stop: cancel}
	controller.Run(ctx, time.Hour, time.Hour, runs, unscheduled{tasks})
	for _, tt := range []struct {
		name            string
		r               *runner
		want, scheduled int
	}{{name: "runs", r: runs, want: 2, scheduled: 1}, {name: "tasks", r: tasks, want: 3}} {
		if tt.r.dispatched != tt.want || tt.r.followed != 1 || tt.r.swept != 1 || tt.r.scheduled != tt.scheduled {
			t.Errorf("one pass of %s dispatched %d, followed %d, swept %d, scheduled %d, want %d, 1, 1, %d", tt.name, tt.r.dispatched,
				tt.r.followed, tt.r.swept, tt.r.scheduled, tt.want, tt.scheduled)
		}
	}
}
