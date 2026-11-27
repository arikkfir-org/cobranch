package live_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/live"
	"cobranch.dev/internal/services/live/livetest"
)

// source is what a test's reads answer, which the test changes.
type source struct {
	mu    sync.Mutex
	value int
	err   error
}

func (s *source) read(context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value, s.err
}

func (s *source) set(value int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value, s.err = value, err
}

// receive answers what ch sends within wait, and whether it sent anything or closed.
func receive(ch <-chan int, wait time.Duration) (value int, sent, closed bool) {
	select {
	case v, ok := <-ch:
		return v, ok, !ok
	case <-time.After(wait):
		return 0, false, false
	}
}

func TestFollow(t *testing.T) {
	t.Parallel()
	watched := uuid.New()
	broken := errors.New("broken")
	tests := []struct {
		name string
		poll time.Duration
		// change changes the source, and wakes those who watch an ID, before the test listens again.
		change     func(s *source, n *livetest.Notifier)
		wantValue  int
		wantSent   bool
		wantClosed bool
	}{
		{name: "a change it hears of", poll: time.Hour, change: func(s *source, n *livetest.Notifier) { s.set(2, nil); n.Wake(watched) },
			wantValue: 2, wantSent: true},
		{name: "a wake that changed nothing", poll: time.Hour, change: func(_ *source, n *livetest.Notifier) { n.Wake(watched) }},
		{name: "a change that names something else", poll: time.Hour, change: func(s *source, n *livetest.Notifier) { s.set(2, nil); n.Wake(uuid.New()) }},
		{name: "a change it missed, read again at the poll", poll: 50 * time.Millisecond, change: func(s *source, _ *livetest.Notifier) { s.set(3, nil) },
			wantValue: 3, wantSent: true},
		{name: "a read that fails", poll: time.Hour, change: func(s *source, n *livetest.Notifier) { s.set(1, broken); n.Wake(watched) },
			wantClosed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, n := &source{value: 1}, &livetest.Notifier{}
			values, err := live.Follow(ctx, n, tt.poll, s.read, watched)
			if err != nil {
				t.Fatal(err)
			}
			if v, sent, _ := receive(values, time.Second); !sent || v != 1 {
				t.Fatalf("first = %d (sent %t); want 1", v, sent)
			}
			tt.change(s, n)
			v, sent, closed := receive(values, 500*time.Millisecond)
			if v != tt.wantValue || sent != tt.wantSent || closed != tt.wantClosed {
				t.Errorf("after the change = %d (sent %t, closed %t); want %d (sent %t, closed %t)", v, sent, closed, tt.wantValue, tt.wantSent, tt.wantClosed)
			}
		})
	}
}

func TestFollowStops(t *testing.T) {
	t.Parallel()
	watched := uuid.New()
	broken := errors.New("broken")
	tests := []struct {
		name    string
		readErr error
		cancel  bool
	}{
		{name: "the first read fails", readErr: broken},
		{name: "the follower leaves", cancel: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s, n := &source{value: 1, err: tt.readErr}, &livetest.Notifier{}
			values, err := live.Follow(ctx, n, time.Hour, s.read, watched)
			if !errors.Is(err, tt.readErr) {
				t.Fatalf("error = %v; want %v", err, tt.readErr)
			}
			if tt.cancel {
				<-values
				cancel()
				if _, _, closed := receive(values, time.Second); !closed {
					t.Fatal("the channel stayed open after the follower left")
				}
			}
			deadline := time.Now().Add(time.Second)
			for n.Watching(watched) != 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if got := n.Watching(watched); got != 0 {
				t.Errorf("watchers left = %d; want 0", got)
			}
		})
	}
}

func TestFollowWithoutNotifier(t *testing.T) {
	t.Parallel()
	s := &source{value: 1}
	values, err := live.Follow(t.Context(), nil, 50*time.Millisecond, s.read)
	if err != nil {
		t.Fatal(err)
	}
	<-values
	s.set(2, nil)
	if v, sent, _ := receive(values, time.Second); !sent || v != 2 {
		t.Errorf("after a poll = %d (sent %t); want 2", v, sent)
	}
}
