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
			controller.Run(ctx, "runs", r, time.Hour, time.Hour)
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
	controller.Run(ctx, "runs", r, 10*time.Millisecond, 50*time.Millisecond)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.followed < 5 || r.swept < 2 || r.swept >= r.followed || r.scheduled != 1 {
		t.Errorf("in 200ms, followed %d times, swept %d and scheduled %d, want every 10ms, every 50ms and once a minute", r.followed,
			r.swept, r.scheduled)
	}
}

// plain is a runner that queues no schedules, as the deliverer.
type plain struct{ r *runner }

func (p plain) Dispatch(ctx context.Context) (bool, error) { return p.r.Dispatch(ctx) }
func (p plain) Follow(ctx context.Context) error           { return p.r.Follow(ctx) }
func (p plain) Sweep(ctx context.Context) error            { return p.r.Sweep(ctx) }

func TestRunWithoutSchedules(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &runner{queue: []error{nil}, stop: cancel}
	controller.Run(ctx, "deliveries", plain{r}, time.Hour, time.Hour)
	if r.dispatched != 2 || r.followed != 1 || r.swept != 1 || r.scheduled != 0 {
		t.Errorf("one pass dispatched %d, followed %d, swept %d, scheduled %d, want 2, 1, 1, 0", r.dispatched, r.followed, r.swept,
			r.scheduled)
	}
}
