package issues_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"cobranch.dev/internal/services/issues"
	"cobranch.dev/internal/services/live"
	"cobranch.dev/internal/services/live/livetest"
	"cobranch.dev/internal/services/orgs"
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

// followList follows Acme's open issues as v, and answers a function that names the titles of those it sends next.
func followList(ctx context.Context, t *testing.T, f *fixture, v issues.Viewer) (func() string, error) {
	pages, err := f.service.FollowList(ctx, v, f.acme.ID, issues.Filter{})
	if err != nil {
		return nil, fmt.Errorf("following the issues: %w", err)
	}
	return func() string {
		out := []string{}
		for _, i := range sent(t, pages).Issues {
			out = append(out, i.Title)
		}
		return fmt.Sprint(out)
	}, nil
}

// followIssue follows ACME-1 as v, and answers a function that tells the state of what it sends next.
func followIssue(ctx context.Context, t *testing.T, f *fixture, v issues.Viewer) (func() string, error) {
	issue, err := f.service.FollowIssue(ctx, v, f.acme.ID, 1)
	if err != nil {
		return nil, fmt.Errorf("following the issue: %w", err)
	}
	return func() string { return string(sent(t, issue).State) }, nil
}

// followActivity follows ACME-1's activity as v, and answers a function that names the kinds of what it sends next.
func followActivity(ctx context.Context, t *testing.T, f *fixture, v issues.Viewer) (func() string, error) {
	events, err := f.service.FollowActivity(ctx, v, f.acme.ID, 1)
	if err != nil {
		return nil, fmt.Errorf("following the activity: %w", err)
	}
	return func() string {
		out := []string{}
		for _, e := range sent(t, events) {
			out = append(out, e.Kind)
		}
		return fmt.Sprint(out)
	}, nil
}

// followBranches follows ACME-1's branches as v, and answers a function that names those it sends next.
func followBranches(ctx context.Context, t *testing.T, f *fixture, v issues.Viewer) (func() string, error) {
	branches, err := f.service.FollowBranches(ctx, v, f.acme.ID, 1)
	if err != nil {
		return nil, fmt.Errorf("following the branches: %w", err)
	}
	return func() string {
		out := []string{}
		for _, b := range sent(t, branches) {
			out = append(out, b.Repository.Slug+"/"+b.Name)
		}
		return fmt.Sprint(out)
	}, nil
}

// closeFirst has Olivia close ACME-1, a change the followers didn't make.
func closeFirst(ctx context.Context, t *testing.T, f *fixture) {
	t.Helper()
	done := issues.StateDone
	if _, err := f.service.Update(ctx, f.olivia, f.acme.ID, 1, issues.Patch{State: &done}); err != nil {
		t.Fatal(err)
	}
}

func TestFollow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// follow follows one of the issues' reads as v, and answers a function that describes what it sends next.
		follow func(ctx context.Context, t *testing.T, f *fixture, v issues.Viewer) (func() string, error)
		// change changes Acme's issues behind the follower's back.
		change        func(ctx context.Context, t *testing.T, f *fixture)
		before, after string
	}{
		{name: "an issue created elsewhere", follow: followList, change: func(ctx context.Context, t *testing.T, f *fixture) {
			if _, err := f.service.Create(ctx, f.olivia, f.acme.ID, issues.Input{Title: "Second"}); err != nil {
				t.Fatal(err)
			}
		}, before: "[First]", after: "[Second First]"},
		{name: "one closed elsewhere", follow: followList, change: closeFirst, before: "[First]", after: "[]"},
		{name: "the issue closed elsewhere", follow: followIssue, change: closeFirst, before: "todo", after: "done"},
		{name: "a change to its activity", follow: followActivity, change: closeFirst,
			before: "[issue.created]", after: "[issue.created issue.changed]"},
		{name: "a branch linked elsewhere", follow: followBranches, change: func(ctx context.Context, t *testing.T, f *fixture) {
			if _, err := f.service.Link(ctx, f.olivia, f.acme.ID, f.website.ID, "login", 1); err != nil {
				t.Fatal(err)
			}
		}, before: "[]", after: "[website/login]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newFixture(t)
			f.create(t, f.max, issues.Input{Title: "First", State: issues.StateTodo})
			f.store.AddBranch(f.website.ID, "login")
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
			tt.change(ctx, t, f)
			notifier.Wake(f.acme.ID)
			if got := next(); got != tt.after {
				t.Errorf("after the change = %s; want %s", got, tt.after)
			}
		})
	}
}

func TestFollowGone(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newFixture(t)
	issue := f.create(t, f.max, issues.Input{Title: "First"})
	notifier := &livetest.Notifier{}
	f.service.WithNotifier(notifier)
	if _, err := f.service.FollowIssue(ctx, f.max, f.acme.ID, 2); !errors.Is(err, issues.ErrNotFound) || !errors.Is(err, live.ErrGone) {
		t.Errorf("following no issue = %v; want %v and %v", err, issues.ErrNotFound, live.ErrGone)
	}
	followed, err := f.service.FollowIssue(ctx, f.max, f.acme.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	sent(t, followed)
	if err := f.service.Delete(ctx, f.olivia, f.acme.ID, 1, issue.Version); err != nil {
		t.Fatal(err)
	}
	notifier.Wake(f.acme.ID)
	select {
	case got, ok := <-followed:
		if ok {
			t.Errorf("sent %s after it was deleted; want the follower stopped", got.Identifier())
		}
	case <-time.After(5 * time.Second):
		t.Error("the follower went on after the issue was deleted")
	}
}

func TestFollowBranchLink(t *testing.T) {
	t.Parallel()
	link := func(ctx context.Context, t *testing.T, f *fixture) {
		t.Helper()
		if _, err := f.service.Link(ctx, f.olivia, f.acme.ID, f.website.ID, "login", 1); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name string
		// before links the branch, or not, before it is followed; change changes its link behind the follower's back.
		before, change func(ctx context.Context, t *testing.T, f *fixture)
		want           [2]string
	}{
		{name: "a branch linked elsewhere", before: func(context.Context, *testing.T, *fixture) {}, change: link, want: [2]string{"none", "ACME-1"}},
		{name: "a branch unlinked elsewhere", before: link, change: func(ctx context.Context, t *testing.T, f *fixture) {
			if err := f.service.Unlink(ctx, f.olivia, f.acme.ID, f.website.ID, "login"); err != nil {
				t.Fatal(err)
			}
		}, want: [2]string{"ACME-1", "none"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newFixture(t)
			f.create(t, f.max, issues.Input{Title: "First", State: issues.StateTodo})
			f.store.AddBranch(f.website.ID, "login")
			tt.before(ctx, t, f)
			notifier := &livetest.Notifier{}
			f.service.WithNotifier(notifier)
			links, err := f.service.FollowBranchLink(ctx, f.max, f.acme.ID, f.website.ID, "login")
			if err != nil {
				t.Fatal(err)
			}
			named := func() string {
				if l := sent(t, links); l != nil {
					return fmt.Sprintf("%s-%d", l.Issue.Key, l.Issue.Number)
				}
				return "none"
			}
			if got := named(); got != tt.want[0] {
				t.Errorf("first = %s; want %s", got, tt.want[0])
			}
			tt.change(ctx, t, f)
			// A link names the branch's repository, which the follower follows.
			notifier.Wake(f.website.ID)
			if got := named(); got != tt.want[1] {
				t.Errorf("after the change = %s; want %s", got, tt.want[1])
			}
		})
	}
}
