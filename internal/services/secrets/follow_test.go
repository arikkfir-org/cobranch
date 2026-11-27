package secrets_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/live/livetest"
	"cobranch.dev/internal/services/secrets"
)

func TestFollowList(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		repo   func(f *fixture) uuid.UUID
		before []string
		after  []string
	}{
		{name: "the organization's secrets", repo: func(*fixture) uuid.UUID { return uuid.Nil }, before: []string{"npm"}, after: []string{"npm", "pypi"}},
		{name: "a repository's secrets", repo: func(f *fixture) uuid.UUID { return f.website }, before: []string{"deploy"}, after: []string{"deploy", "pypi"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newFixture(t)
			notifier := &livetest.Notifier{}
			f.service.WithNotifier(notifier)
			repo := tt.repo(f)
			if _, err := f.service.FollowList(ctx, f.nia, f.acme, repo); !errors.Is(err, secrets.ErrNotFound) {
				t.Errorf("an outsider follows them: %v; want %v", err, secrets.ErrNotFound)
			}
			lists, err := f.service.FollowList(ctx, f.olivia, f.acme, repo)
			if err != nil {
				t.Fatal(err)
			}
			if got := names(t, lists); fmt.Sprint(got) != fmt.Sprint(tt.before) {
				t.Errorf("first = %v; want %v", got, tt.before)
			}
			if _, err := f.service.Create(ctx, f.olivia, f.acme, repo, secrets.Input{Name: "pypi", Value: "token"}); err != nil {
				t.Fatal(err)
			}
			notifier.Wake(f.acme)
			if got := names(t, lists); fmt.Sprint(got) != fmt.Sprint(tt.after) {
				t.Errorf("after a secret added elsewhere = %v; want %v", got, tt.after)
			}
		})
	}
}

// names names the secrets the follower sends next, or fails after a few seconds.
func names(t *testing.T, lists <-chan []secrets.Secret) []string {
	t.Helper()
	select {
	case list, ok := <-lists:
		if !ok {
			t.Fatal("the follower stopped")
		}
		out := []string{}
		for _, s := range list {
			out = append(out, s.Name)
		}
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("the follower sent nothing")
		return nil
	}
}
