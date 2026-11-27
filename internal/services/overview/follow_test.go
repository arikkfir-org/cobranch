package overview_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"cobranch.dev/internal/services/live/livetest"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/overview"
)

// queued answers how many tasks wait in the Overview ch sends next, or fails after a few seconds.
func queued(t *testing.T, ch <-chan overview.Overview) int64 {
	t.Helper()
	select {
	case o, ok := <-ch:
		if !ok {
			t.Fatal("the follower stopped")
		}
		return o.Queued
	case <-time.After(5 * time.Second):
		t.Fatal("the follower sent nothing")
		return 0
	}
}

// TestFollowOverview checks that a follower of the Overview hears of a change to the organization's figures, and that
// an outsider can't follow it.
func TestFollowOverview(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newFixture(t)
	notifier := &livetest.Notifier{}
	f.service.WithNotifier(notifier)
	if _, err := f.service.FollowOverview(ctx, f.nia, f.org, f.period); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("an outsider follows it: %v; want %v", err, orgs.ErrNotFound)
	}
	figures, err := f.service.FollowOverview(ctx, f.max, f.org, f.period)
	if err != nil {
		t.Fatal(err)
	}
	if got := queued(t, figures); got != 5 {
		t.Errorf("first = %d queued; want 5", got)
	}
	o := f.store.Overviews[f.org]
	o.Queued = 6
	f.store.Overviews[f.org] = o
	notifier.Wake(f.org)
	if got := queued(t, figures); got != 6 {
		t.Errorf("after a task queued elsewhere = %d queued; want 6", got)
	}
}
