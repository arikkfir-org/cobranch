package code_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/live/livetest"
	"cobranch.dev/internal/services/orgs"
)

// sentNext answers what ch sends next, or fails after a few seconds.
func sentNext[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case got, ok := <-ch:
		if !ok {
			t.Fatal("the follower stopped")
		}
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("the follower sent nothing")
		var none T
		return none
	}
}

// followAttention follows the branches that need v, and answers a function that names those it sends next.
func followAttention(ctx context.Context, t *testing.T, f *attentionFixture, v code.Viewer) (func() string, error) {
	ch, err := f.service.FollowAttention(ctx, v, f.org)
	if err != nil {
		return nil, fmt.Errorf("following what needs the viewer: %w", err)
	}
	return func() string {
		out := []string{}
		for _, a := range sentNext(t, ch) {
			out = append(out, a.Repo+"/"+a.Name+":"+string(a.Need))
		}
		return fmt.Sprint(out)
	}, nil
}

// followActivity follows the organization's newest activity as v, and answers a function that names the repositories
// of what it sends next.
func followActivity(ctx context.Context, t *testing.T, f *attentionFixture, v code.Viewer) (func() string, error) {
	ch, err := f.service.FollowActivity(ctx, v, f.org, code.ActivityCursor{}, 0)
	if err != nil {
		return nil, fmt.Errorf("following the activity: %w", err)
	}
	return func() string { return fmt.Sprint(activityRepos(sentNext(t, ch).Activity)) }, nil
}

func TestFollow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// follow follows what the test checks as v, and answers a function that describes what the follower sends next.
		follow func(ctx context.Context, t *testing.T, f *attentionFixture, v code.Viewer) (func() string, error)
		// change changes the organization behind the follower's back.
		change func(f *attentionFixture)
		before string
		after  string
	}{
		{name: "a branch that comes to need the viewer",
			follow: followAttention,
			change: func(f *attentionFixture) { f.branch(f.website, "fix", f.ann, code.StatusReady, 1) },
			before: "[]", after: "[website/fix:merge]"},
		{name: "a push elsewhere",
			follow: followActivity,
			change: func(f *attentionFixture) {
				f.store.ActivityList[f.org] = append(f.store.ActivityList[f.org], code.Activity{ID: uuid.New(), At: now, Kind: code.ActivityPushed,
					RepoID: f.book, Repo: "handbook"})
			},
			before: "[]", after: "[handbook]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newAttentionFixture()
			notifier := &livetest.Notifier{}
			f.service.WithNotifier(notifier)
			if _, err := tt.follow(ctx, t, f, f.nia); !errors.Is(err, orgs.ErrNotFound) {
				t.Errorf("an outsider follows them: %v; want %v", err, orgs.ErrNotFound)
			}
			next, err := tt.follow(ctx, t, f, f.max)
			if err != nil {
				t.Fatal(err)
			}
			if got := next(); got != tt.before {
				t.Errorf("first = %s; want %s", got, tt.before)
			}
			tt.change(f)
			notifier.Wake(f.org)
			if got := next(); got != tt.after {
				t.Errorf("after the change = %s; want %s", got, tt.after)
			}
		})
	}
}
