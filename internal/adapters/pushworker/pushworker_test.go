package pushworker_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"cobranch.dev/internal/adapters/pushworker"
)

// step is what one Process call answers.
type step struct {
	processed bool
	err       error
}

// processor answers its steps in order, then that nothing waits, and reports each call on calls.
type processor struct {
	mu    sync.Mutex
	steps []step
	calls chan int
	n     int
}

func (p *processor) Process(context.Context) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.n++
	defer func() { p.calls <- p.n }()
	if len(p.steps) == 0 {
		return false, nil
	}
	s := p.steps[0]
	p.steps = p.steps[1:]
	return s.processed, s.err
}

// left is how many steps no call answered yet.
func (p *processor) left() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.steps)
}

// add queues more steps, as pushes the Git server records later.
func (p *processor) add(steps ...step) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.steps = append(p.steps, steps...)
}

var errDatabase = errors.New("database down")

// runCase is one of TestRun's cases.
type runCase struct {
	name string
	// steps wait when Run starts; later arrive once it settled, with a wake when wake is set.
	steps, later []step
	wake         bool
	// poll is Run's poll interval: an hour, unless the case needs it.
	poll time.Duration
	// wantCalls and wantLeft are the Process calls and the steps left once Run first settled; a negative wantCalls
	// doesn't count them.
	wantCalls, wantLeft int
}

func TestRun(t *testing.T) {
	pushed := step{processed: true}
	tests := []runCase{
		{name: "drains what waits, then waits", steps: []step{pushed, pushed}, wantCalls: 3},
		{name: "a push it failed doesn't stop the rest", steps: []step{{processed: true, err: errDatabase}, pushed}, wantCalls: 3},
		{name: "a database that fails waits for the next wake", steps: []step{{err: errDatabase}, pushed}, wake: true, wantCalls: 1, wantLeft: 1},
		{name: "a wake processes a new push", later: []step{pushed}, wake: true, wantCalls: 1},
		{name: "the poll finds a push whose wake was lost", later: []step{pushed}, poll: 20 * time.Millisecond, wantCalls: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.run(t) })
	}
}

func (tt runCase) run(t *testing.T) {
	p := &processor{steps: slices.Clone(tt.steps), calls: make(chan int, 1000)}
	wake := make(chan struct{}, 1)
	poll := tt.poll
	if poll == 0 {
		poll = time.Hour
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		pushworker.Run(ctx, p, wake, poll)
		close(done)
	}()
	if tt.wantCalls >= 0 {
		if calls := settle(p.calls); calls != tt.wantCalls || p.left() != tt.wantLeft {
			t.Errorf("Run() made %d calls and left %d steps, want %d and %d", calls, p.left(), tt.wantCalls, tt.wantLeft)
		}
	}
	p.add(tt.later...)
	if tt.wake {
		wake <- struct{}{}
	}
	deadline := time.Now().Add(5 * time.Second)
	for p.left() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if p.left() > 0 {
		t.Errorf("Run() left %d steps", p.left())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run() didn't return once its context ended")
	}
}

// settle waits until Process goes unanswered for a while, and returns how many calls it had by then.
func settle(calls <-chan int) int {
	n := 0
	for {
		select {
		case n = <-calls:
		case <-time.After(200 * time.Millisecond):
			return n
		}
	}
}
