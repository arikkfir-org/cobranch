package repos_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/live/livetest"
	"cobranch.dev/internal/services/repos"
)

// sent answers what ch sends next, or fails after a few seconds.
func sent[T any](t *testing.T, ch <-chan T) T {
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

// slugs are the slugs of the repositories in list.
func slugs(list []repos.Listed) []string {
	out := []string{}
	for _, r := range list {
		out = append(out, r.Slug)
	}
	return out
}

// followList follows the organization's repositories as v, and answers a function that names those it sends next.
func followList(ctx context.Context, t *testing.T, f *fixture, v repos.Viewer) (func() string, error) {
	ch, err := f.service.FollowList(ctx, v, f.acme.ID)
	if err != nil {
		return nil, fmt.Errorf("following the repositories: %w", err)
	}
	return func() string { return fmt.Sprint(slugs(sent(t, ch))) }, nil
}

// followUsage follows the organization's usage as v, and answers a function that tells the Git data it sends next.
func followUsage(ctx context.Context, t *testing.T, f *fixture, v repos.Viewer) (func() string, error) {
	ch, err := f.service.FollowUsage(ctx, v, f.acme.ID)
	if err != nil {
		return nil, fmt.Errorf("following the usage: %w", err)
	}
	return func() string { return fmt.Sprint(sent(t, ch).GitData.Used) }, nil
}

// followWebsite follows website as v, and answers a function that tells the name and the description of what it sends
// next.
func followWebsite(ctx context.Context, t *testing.T, f *fixture, v repos.Viewer) (func() string, error) {
	ch, err := f.service.FollowRepository(ctx, v, f.acme.ID, "website")
	if err != nil {
		return nil, fmt.Errorf("following website: %w", err)
	}
	return func() string { r := sent(t, ch).Repository; return r.Name + ": " + r.Description }, nil
}

// followAccess follows who holds roles on website as v, and answers a function that names, sorted, those it sends next.
func followAccess(ctx context.Context, t *testing.T, f *fixture, v repos.Viewer) (func() string, error) {
	ch, err := f.service.FollowAccess(ctx, v, f.acme.ID, f.website.ID)
	if err != nil {
		return nil, fmt.Errorf("following website's access: %w", err)
	}
	return func() string {
		out := []string{}
		for _, a := range sent(t, ch) {
			out = append(out, a.Principal.DisplayName)
		}
		slices.Sort(out)
		return fmt.Sprint(out)
	}, nil
}

func TestFollowRepositories(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// follow follows what the test checks as v, and answers a function that describes what the follower sends next.
		follow func(ctx context.Context, t *testing.T, f *fixture, v repos.Viewer) (func() string, error)
		// change changes the organization's repositories behind the follower's back.
		change func(t *testing.T, f *fixture)
		before string
		after  string
	}{
		{name: "a repository created elsewhere",
			follow: followList,
			change: func(t *testing.T, f *fixture) {
				if _, err := f.service.Create(t.Context(), f.olivia, f.acme.ID, repos.Input{Slug: "docs"}); err != nil {
					t.Fatal(err)
				}
			}, before: "[secret website]", after: "[docs secret website]"},
		{name: "a push that grows the Git data",
			follow: followUsage,
			change: func(_ *testing.T, f *fixture) { f.store.GitData[f.website.ID] = 1500 },
			before: "1000", after: "1500"},
		{name: "a repository described elsewhere",
			follow: followWebsite,
			change: func(t *testing.T, f *fixture) {
				if _, err := f.service.Update(t.Context(), f.olivia, f.website.ID, repos.Input{Slug: "website", Name: "x", Description: "The site"}); err != nil {
					t.Fatal(err)
				}
			}, before: "x: ", after: "x: The site"},
		{name: "a role granted on the repository",
			follow: followAccess,
			change: func(t *testing.T, f *fixture) {
				if err := f.service.Grant(t.Context(), f.olivia, f.website.ID, f.gwen.ID, auth.RoleDeveloper); err != nil {
					t.Fatal(err)
				}
			}, before: "[Max Olivia Rita]", after: "[Gwen Max Olivia Rita]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newFixture(t)
			f.store.GitData[f.website.ID] = 1000
			notifier := &livetest.Notifier{}
			f.service.WithNotifier(notifier)
			if _, err := tt.follow(ctx, t, f, f.nia); !errors.Is(err, repos.ErrNotFound) {
				t.Errorf("an outsider follows them: %v; want %v", err, repos.ErrNotFound)
			}
			next, err := tt.follow(ctx, t, f, f.olivia)
			if err != nil {
				t.Fatal(err)
			}
			if got := next(); got != tt.before {
				t.Errorf("first = %s; want %s", got, tt.before)
			}
			tt.change(t, f)
			notifier.Wake(f.acme.ID)
			if got := next(); got != tt.after {
				t.Errorf("after the change = %s; want %s", got, tt.after)
			}
		})
	}
}
