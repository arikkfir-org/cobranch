package code_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/jobs"
	livefollow "cobranch.dev/internal/services/live"
	"cobranch.dev/internal/services/live/livetest"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
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

// pushTo pushes a commit with message to website's branch name, on top of the commit it was at, and answers its SHA.
func (f *attentionFixture) pushTo(name, message string) string {
	f.n++
	head := fmt.Sprintf("%040x", f.n)
	c := code.Commit{SHA: head, Message: message, Author: code.Person{Name: "A"}}
	for _, b := range f.store.BranchList[f.website] {
		if b.Name == name {
			c.Parents = []string{b.Head}
		}
	}
	f.store.Add(f.website, code.Push{ID: uuid.New(), Transport: git.TransportHTTPS, At: now}, c)
	f.store.SetBranch(f.website, name, head)
	return head
}

// followBranches follows website's branches as v, and answers a function that names those it sends next.
func followBranches(ctx context.Context, t *testing.T, f *attentionFixture, v code.Viewer) (func() string, error) {
	ch, err := f.service.FollowBranches(ctx, v, f.website)
	if err != nil {
		return nil, fmt.Errorf("following the branches: %w", err)
	}
	return func() string {
		out := []string{}
		for _, b := range sentNext(t, ch) {
			out = append(out, b.Name)
		}
		return fmt.Sprint(out)
	}, nil
}

// followMain follows website's main as v, and answers a function that tells the message of the head it sends next.
func followMain(ctx context.Context, t *testing.T, f *attentionFixture, v code.Viewer) (func() string, error) {
	ch, err := f.service.FollowBranch(ctx, v, f.website, "main")
	if err != nil {
		return nil, fmt.Errorf("following main: %w", err)
	}
	return func() string { return sentNext(t, ch).Commit.Message }, nil
}

// followHistory follows main's history as v, and answers a function that tells the messages of what it sends next.
func followHistory(ctx context.Context, t *testing.T, f *attentionFixture, v code.Viewer) (func() string, error) {
	ch, err := f.service.FollowHistory(ctx, v, f.website, code.HistoryQuery{Branch: "main"})
	if err != nil {
		return nil, fmt.Errorf("following main's history: %w", err)
	}
	return func() string {
		out := []string{}
		for _, c := range sentNext(t, ch).Commits {
			out = append(out, c.Message)
		}
		return fmt.Sprint(out)
	}, nil
}

// followReviews follows the reviews of main's head as v, and answers a function that tells the states of what it
// sends next.
func followReviews(ctx context.Context, t *testing.T, f *attentionFixture, v code.Viewer) (func() string, error) {
	ch, err := f.service.FollowReviews(ctx, v, f.website, f.head("main"), code.ReviewQuery{})
	if err != nil {
		return nil, fmt.Errorf("following the reviews: %w", err)
	}
	return func() string {
		out := []string{}
		for _, r := range sentNext(t, ch).Reviews {
			out = append(out, string(r.State))
		}
		return fmt.Sprint(out)
	}, nil
}

// followAnnotations follows the annotations on main's head as v, and answers a function that names those it sends
// next.
func followAnnotations(ctx context.Context, t *testing.T, f *attentionFixture, v code.Viewer) (func() string, error) {
	ch, err := f.service.FollowAnnotations(ctx, v, f.website, f.head("main"))
	if err != nil {
		return nil, fmt.Errorf("following the annotations: %w", err)
	}
	return func() string {
		out := []string{}
		for _, a := range sentNext(t, ch) {
			out = append(out, a.Name)
		}
		return fmt.Sprint(out)
	}, nil
}

// head answers the head of website's branch name.
func (f *attentionFixture) head(name string) string {
	for _, b := range f.store.BranchList[f.website] {
		if b.Name == name {
			return b.Head
		}
	}
	return ""
}

// followRuns follows website's runs as v, and answers a function that counts those it sends next.
func followRuns(ctx context.Context, t *testing.T, f *attentionFixture, v code.Viewer) (func() string, error) {
	ch, err := f.service.FollowRuns(ctx, v, f.website, code.RunsQuery{})
	if err != nil {
		return nil, fmt.Errorf("following the runs: %w", err)
	}
	return func() string { return fmt.Sprint(len(sentNext(t, ch).Runs)) }, nil
}

func TestFollowRepository(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// follow follows what the test checks of website as v, and answers a function that describes what the follower
		// sends next.
		follow        func(ctx context.Context, t *testing.T, f *attentionFixture, v code.Viewer) (func() string, error)
		change        func(f *attentionFixture)
		before, after string
	}{
		{name: "a branch opened elsewhere", follow: followBranches, change: func(f *attentionFixture) { f.pushTo("fix", "Fix") },
			before: "[main]", after: "[main fix]"},
		{name: "a push to the branch", follow: followMain, change: func(f *attentionFixture) { f.pushTo("main", "Second") },
			before: "First", after: "Second"},
		{name: "a push to its history", follow: followHistory, change: func(f *attentionFixture) { f.pushTo("main", "Second") },
			before: "[First]", after: "[Second First]"},
		{name: "a review of its head", follow: followReviews, change: func(f *attentionFixture) {
			if _, err := f.store.SubmitReview(context.Background(), uuid.New(), f.website, f.head("main"), code.ReviewInput{State: code.ReviewApproved}); err != nil {
				panic(err)
			}
		}, before: "[]", after: "[approved]"},
		{name: "an annotation on its head", follow: followAnnotations, change: func(f *attentionFixture) {
			in := code.AnnotationInput{Name: "ci/test", Status: code.AnnotationSuccessful}
			if _, err := f.store.SetAnnotation(context.Background(), uuid.New(), f.website, f.head("main"), in); err != nil {
				panic(err)
			}
		}, before: "[]", after: "[ci/test]"},
		{name: "a run queued", follow: followRuns, change: func(f *attentionFixture) {
			f.store.AddRun(f.website, code.Run{State: jobs.StateQueued, SHA: f.pushTo("main", "Second")})
		}, before: "0", after: "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newAttentionFixture()
			f.pushTo("main", "First")
			notifier := &livetest.Notifier{}
			f.service.WithNotifier(notifier)
			if _, err := tt.follow(ctx, t, f, f.nia); !errors.Is(err, repos.ErrNotFound) || !errors.Is(err, livefollow.ErrGone) {
				t.Errorf("an outsider follows it: %v; want %v and %v", err, repos.ErrNotFound, livefollow.ErrGone)
			}
			next, err := tt.follow(ctx, t, f, f.max)
			if err != nil {
				t.Fatal(err)
			}
			if got := next(); got != tt.before {
				t.Errorf("first = %s; want %s", got, tt.before)
			}
			tt.change(f)
			notifier.Wake(f.website)
			if got := next(); got != tt.after {
				t.Errorf("after the change = %s; want %s", got, tt.after)
			}
		})
	}
}
