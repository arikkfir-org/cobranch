package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/issues"
)

// linkFixture is gitFixture with issues' store, and Acme's issues ACME-1 in Backlog and ACME-2 in Todo, by Olivia.
type linkFixture struct {
	*gitFixture
	is            *IssueStore
	first, second issues.Issue
}

func newLinkFixture(t *testing.T) *linkFixture {
	t.Helper()
	f := &linkFixture{gitFixture: newGitFixture(t)}
	f.is = NewIssueStore(f.pool)
	f.first = f.createIssue(t, "Log in", issues.StateBacklog)
	f.second = f.createIssue(t, "Sign up", issues.StateTodo)
	return f
}

func (f *linkFixture) createIssue(t *testing.T, title string, state issues.State) issues.Issue {
	t.Helper()
	issue, err := f.is.Create(context.Background(), f.olivia, issues.Issue{ID: uuid.Must(uuid.NewV7()), OrgID: f.acme.ID, Title: title, State: state,
		CreatedBy: &issues.Principal{ID: f.olivia}, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	return issue
}

// push records Max's push into website of updates, bringing heads into main, and processes it.
func (f *linkFixture) push(t *testing.T, updates []git.RefUpdate, merged ...git.Head) {
	t.Helper()
	f.record(t, updates, merged...)
	f.process(t)
}

// record records Max's push into website of updates, bringing heads into main, for the push worker to process.
func (f *linkFixture) record(t *testing.T, updates []git.RefUpdate, merged ...git.Head) {
	t.Helper()
	p := pushOf(f.website.ID, f.max, "", updates)
	if len(merged) > 0 {
		p.Brought = []git.Brought{{Branch: git.DefaultBranch, Heads: merged}}
	}
	if err := f.g.Record(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func (f *linkFixture) issue(t *testing.T, id uuid.UUID) issues.Issue {
	t.Helper()
	issue, err := f.is.ByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return issue
}

// linked is the issue's branches, as "name by how", deleted ones marked.
func (f *linkFixture) linked(t *testing.T, issueID uuid.UUID) []string {
	t.Helper()
	branches, err := f.is.Branches(context.Background(), issueID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, b := range branches {
		s := b.Repository.Slug + "/" + b.Name + " by " + string(b.LinkedBy)
		if b.Deleted {
			s += ", deleted"
		}
		out = append(out, s)
	}
	return out
}

// shows fails t unless the issue, read alone and in Acme's list, shows branch as its live branch, "repository/name",
// or none for "".
func (f *linkFixture) shows(t *testing.T, issueID uuid.UUID, branch string) {
	t.Helper()
	list, _, err := f.is.List(context.Background(), f.acme.ID, issues.Query{States: issues.States, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	listed := slices.IndexFunc(list, func(i issues.Issue) bool { return i.ID == issueID })
	for how, issue := range map[string]issues.Issue{"ByID": f.issue(t, issueID), "List": list[listed]} {
		got := ""
		if b := issue.Branch; b != nil {
			if b.Repository.ID != f.website.ID {
				t.Errorf("%s: ACME-%d's branch is in repository %s, want website's", how, issue.Number, b.Repository.ID)
			}
			got = b.Repository.Slug + "/" + b.Name
		}
		if got != branch {
			t.Errorf("%s: ACME-%d's branch = %q, want %q", how, issue.Number, got, branch)
		}
	}
}

func (f *linkFixture) events(t *testing.T, issueID uuid.UUID) []issues.Event {
	t.Helper()
	events, err := f.is.Activity(context.Background(), issueID)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func created(name, sha string) git.RefUpdate {
	return git.RefUpdate{Name: "refs/heads/" + name, Old: git.ZeroSHA, New: sha}
}

func TestBranchLinksByName(t *testing.T) {
	t.Parallel()
	f := newLinkFixture(t)
	ctx := context.Background()
	f.push(t, []git.RefUpdate{created("main", shaA), created("acme-1-login", shaA), created("alice/ACME-3", shaA), created("feature", shaA)})
	if got := f.linked(t, f.first.ID); !slices.Equal(got, []string{"website/acme-1-login by name"}) {
		t.Fatalf("ACME-1's branches = %v", got)
	}
	got := f.issue(t, f.first.ID)
	if got.State != issues.StateInProgress || got.Version != 2 {
		t.Fatalf("ACME-1 after its branch's push = %s at version %d, want in_progress at 2", got.State, got.Version)
	}
	events := f.events(t, f.first.ID)
	if len(events) != 3 || events[1].Kind != "issue.branch_linked" || events[1].Actor == nil || events[1].Actor.ID != f.max ||
		events[1].Branch == nil || *events[1].Branch != (issues.EventBranch{RepositoryID: f.website.ID, Repository: "website", Name: "acme-1-login",
		LinkedBy: issues.LinkedByName}) || events[2].Kind != "issue.changed" || events[2].Changes[0] != (issues.Change{Field: "state", From: "backlog",
		To: "in_progress"}) {
		t.Fatalf("ACME-1's activity = %+v", events)
	}
	// A branch that names an issue to come links when the issue is created, which starts it.
	third := f.createIssue(t, "Later", issues.StateBacklog)
	if third.Number != 3 || third.State != issues.StateInProgress || !slices.Equal(f.linked(t, third.ID), []string{"website/alice/ACME-3 by name"}) {
		t.Fatalf("ACME-3 = %+v, branches %v", third, f.linked(t, third.ID))
	}
	if n := count(t, f.pool, "SELECT count(*) FROM branch_issues"); n != 2 {
		t.Fatalf("%d links, want 2: main and feature name no issue", n)
	}
	// Another push into a linked branch changes nothing.
	f.push(t, []git.RefUpdate{{Name: "refs/heads/acme-1-login", Old: shaA, New: shaB}})
	if n := len(f.events(t, f.first.ID)); n != 3 {
		t.Fatalf("ACME-1 has %d events after another push, want 3", n)
	}
	if _, err := f.is.ByID(ctx, uuid.New()); !errors.Is(err, issues.ErrNotFound) {
		t.Fatalf("ByID() of no issue = %v", err)
	}
}

func TestBranchLinksByHand(t *testing.T) {
	t.Parallel()
	f := newLinkFixture(t)
	ctx := context.Background()
	f.push(t, []git.RefUpdate{created("main", shaA), created("acme-1-login", shaA), created("feature", shaA)})
	feature, err := f.is.Branch(ctx, f.acme.ID, f.website.ID, "feature")
	if err != nil || feature.IssueID != uuid.Nil || feature.LinkedBy != "" || feature.Repository.Slug != "website" {
		t.Fatalf("Branch(feature) = %+v, %v", feature, err)
	}
	if err := f.is.Link(ctx, f.olivia, feature, &f.second.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.issue(t, f.second.ID); got.State != issues.StateInProgress || !slices.Equal(f.linked(t, f.second.ID), []string{"website/feature by hand"}) {
		t.Fatalf("ACME-2 after the link = %s, %v", got.State, f.linked(t, f.second.ID))
	}
	f.shows(t, f.second.ID, "website/feature")
	// Relinking a branch linked by its name unlinks it from that issue, and doesn't move an issue that has started.
	login, err := f.is.Branch(ctx, f.acme.ID, f.website.ID, "acme-1-login")
	if err != nil || login.IssueID != f.first.ID || login.LinkedBy != issues.LinkedByName {
		t.Fatalf("Branch(acme-1-login) = %+v, %v", login, err)
	}
	if err := f.is.Link(ctx, f.olivia, login, &f.second.ID); err != nil {
		t.Fatal(err)
	}
	if events := f.events(t, f.first.ID); events[len(events)-1].Kind != "issue.branch_unlinked" || events[len(events)-1].Actor.ID != f.olivia {
		t.Fatalf("ACME-1's activity = %+v, want the unlink last", events)
	}
	if got := f.linked(t, f.second.ID); !slices.Equal(got, []string{"website/acme-1-login by hand", "website/feature by hand"}) {
		t.Fatalf("ACME-2's branches = %v", got)
	}
	// An issue shows the live branch linked to it last.
	f.shows(t, f.first.ID, "")
	f.shows(t, f.second.ID, "website/acme-1-login")
	if n := len(f.events(t, f.second.ID)); n != 4 {
		t.Fatalf("ACME-2 has %d events, want created, two links and one change", n)
	}
	// Unlinked by hand, a branch stays unlinked whatever its pushes.
	if err := f.is.Link(ctx, f.olivia, login, nil); err != nil {
		t.Fatal(err)
	}
	f.push(t, []git.RefUpdate{{Name: "refs/heads/acme-1-login", Old: shaA, New: shaB}})
	if got, err := f.is.Branch(ctx, f.acme.ID, f.website.ID, "acme-1-login"); err != nil || got.IssueID != uuid.Nil || got.LinkedBy != issues.LinkedByHand {
		t.Fatalf("Branch(acme-1-login) after the unlink and a push = %+v, %v", got, err)
	}
	// A deleted branch keeps its link, and can't be linked.
	f.push(t, []git.RefUpdate{{Name: "refs/heads/feature", Old: shaA, New: git.ZeroSHA}})
	if got := f.linked(t, f.second.ID); !slices.Equal(got, []string{"website/feature by hand, deleted"}) {
		t.Fatalf("ACME-2's branches after the delete = %v", got)
	}
	f.shows(t, f.second.ID, "")
	if _, err := f.is.Branch(ctx, f.acme.ID, f.website.ID, "feature"); !errors.Is(err, issues.ErrBranchNotFound) {
		t.Fatalf("Branch() of a deleted branch = %v", err)
	}
	if err := f.is.Link(ctx, f.olivia, feature, &f.first.ID); !errors.Is(err, issues.ErrBranchNotFound) {
		t.Fatalf("Link() of a deleted branch = %v", err)
	}
}

func TestBranchLinksMerge(t *testing.T) {
	t.Parallel()
	f := newLinkFixture(t)
	f.push(t, []git.RefUpdate{created("main", shaA), created("acme-1-login", shaB), created("acme-2-sign-up", shaB)})
	canceled := f.issue(t, f.second.ID)
	canceled.State, canceled.ClosedAt = issues.StateCanceled, &now
	if _, err := f.is.Update(context.Background(), f.olivia, canceled, canceled.Version, nil); err != nil {
		t.Fatal(err)
	}
	f.push(t, []git.RefUpdate{{Name: "refs/heads/main", Old: shaA, New: shaB}},
		git.Head{Branch: "acme-1-login", SHA: shaB}, git.Head{Branch: "acme-2-sign-up", SHA: shaB})
	first := f.issue(t, f.first.ID)
	if first.State != issues.StateDone || first.ClosedAt == nil {
		t.Fatalf("ACME-1 after its branch's merge = %s, closed %v", first.State, first.ClosedAt)
	}
	events := f.events(t, f.first.ID)
	if last := events[len(events)-1]; last.Kind != "issue.changed" || last.Actor.ID != f.max ||
		last.Changes[0] != (issues.Change{Field: "state", From: "in_progress", To: "done"}) {
		t.Fatalf("ACME-1's last event = %+v", last)
	}
	if got := f.issue(t, f.second.ID); got.State != issues.StateCanceled {
		t.Fatalf("a canceled issue's merge moved it to %s", got.State)
	}
}

// A merge finishes the issue its branch was linked to though the branch is deleted before the push worker gets to the
// merge: by the push that merges it, or by the next.
func TestBranchLinksMergeOfDeletedBranch(t *testing.T) {
	t.Parallel()
	deletion := git.RefUpdate{Name: "refs/heads/acme-1-login", Old: shaB, New: git.ZeroSHA}
	for _, tc := range []struct {
		name   string
		pushes [][]git.RefUpdate
	}{
		{name: "by the merging push", pushes: [][]git.RefUpdate{{{Name: "refs/heads/main", Old: shaA, New: shaB}, deletion}}},
		{name: "by the next push", pushes: [][]git.RefUpdate{{{Name: "refs/heads/main", Old: shaA, New: shaB}}, {deletion}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newLinkFixture(t)
			f.push(t, []git.RefUpdate{created("main", shaA), created("acme-1-login", shaB)})
			f.record(t, tc.pushes[0], git.Head{Branch: "acme-1-login", SHA: shaB})
			for _, updates := range tc.pushes[1:] {
				f.record(t, updates)
			}
			if n := f.process(t); n != len(tc.pushes) {
				t.Fatalf("processed %d pushes, want %d", n, len(tc.pushes))
			}
			if got := f.issue(t, f.first.ID); got.State != issues.StateDone || got.ClosedAt == nil {
				t.Fatalf("ACME-1 after its deleted branch's merge = %s, closed %v", got.State, got.ClosedAt)
			}
			if got := f.linked(t, f.first.ID); !slices.Equal(got, []string{"website/acme-1-login by name, deleted"}) {
				t.Fatalf("ACME-1's branches = %v", got)
			}
		})
	}
}
