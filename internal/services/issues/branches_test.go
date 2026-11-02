package issues_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/issues"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

func TestNamed(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		want      int
		ok        bool
	}{
		{"acme-42-login", "ACME", 42, true},
		{"alice/ACME-42", "ACME", 42, true},
		{"fix/acme-42_retry", "ACME", 42, true},
		{"ACME-7", "ACME", 7, true},
		{"acme-420", "ACME", 420, true},
		{"acme-42-then-acme-7", "ACME", 42, true},
		{"xacme-42", "ACME", 0, false},
		{"acme-42x", "ACME", 0, false},
		{"acme-0", "ACME", 0, false},
		{"acme-", "ACME", 0, false},
		{"acme-99999999999", "ACME", 0, false},
		{"globe-3", "ACME", 0, false},
		{"main", "ACME", 0, false},
		{"acme-1", "", 0, false},
		{"a1-5", "A1", 5, true},
		{"über/acme-42", "ACME", 42, true},
		{"acme-42—retry", "ACME", 42, true},
		{"caféacme-42", "ACME", 0, false},
		{"acme-42é", "ACME", 0, false},
		{"acme-42٣", "ACME", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := issues.Named(tc.name, tc.key)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("Named(%q, %q) = %d, %v, want %d, %v", tc.name, tc.key, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// writer is Wren, of a custom role of Acme that writes issues and reads repositories but reads no issues.
func (f *fixture) writer() issues.Viewer {
	wren := user("Wren")
	f.orgs.AddUser(wren.ID, wren.DisplayName)
	role := orgs.Role{ID: uuid.New(), Name: "Writer", OrgID: f.acme.ID, Permissions: []string{orgs.PermRead, issues.PermWrite, repos.PermRead}}
	f.orgs.RoleList = append(f.orgs.RoleList, role)
	f.orgs.AddMember(f.acme.ID, wren.ID, role.ID)
	return wren
}

func TestLink(t *testing.T) {
	for _, tc := range []struct {
		name   string
		viewer func(f *fixture) issues.Viewer
		branch string
		repo   func(f *fixture) uuid.UUID
		number int
		want   error
		state  issues.State
	}{
		{"a member links a branch", func(f *fixture) issues.Viewer { return f.max }, "login", func(f *fixture) uuid.UUID { return f.website.ID }, 1, nil,
			issues.StateInProgress},
		{"an issue done already stays done", func(f *fixture) issues.Viewer { return f.max }, "login", func(f *fixture) uuid.UUID { return f.website.ID }, 2,
			nil, issues.StateDone},
		{"a guest", func(f *fixture) issues.Viewer { return f.gwen }, "login", func(f *fixture) uuid.UUID { return f.website.ID }, 1, auth.ErrForbidden,
			issues.StateBacklog},
		{"a writer who reads no issues", (*fixture).writer, "login", func(f *fixture) uuid.UUID { return f.website.ID }, 1, nil, issues.StateInProgress},
		{"a repository the viewer doesn't read", func(f *fixture) issues.Viewer { return f.max }, "deploy", func(f *fixture) uuid.UUID { return f.secret.ID },
			1, issues.ErrBranchNotFound, issues.StateBacklog},
		{"no such branch", func(f *fixture) issues.Viewer { return f.max }, "nope", func(f *fixture) uuid.UUID { return f.website.ID }, 1,
			issues.ErrBranchNotFound, issues.StateBacklog},
		{"the default branch", func(f *fixture) issues.Viewer { return f.olivia }, "main", func(f *fixture) uuid.UUID { return f.website.ID }, 1,
			&orgs.ValidationError{}, issues.StateBacklog},
		{"no such issue", func(f *fixture) issues.Viewer { return f.max }, "login", func(f *fixture) uuid.UUID { return f.website.ID }, 9,
			&orgs.ValidationError{}, issues.StateBacklog},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			first := f.create(t, f.olivia, issues.Input{Title: "Log in"})
			done := f.create(t, f.olivia, issues.Input{Title: "Done", State: issues.StateDone})
			f.store.AddBranch(f.website.ID, "login")
			f.store.AddBranch(f.website.ID, "main")
			f.store.AddBranch(f.secret.ID, "deploy")
			ctx := context.Background()
			link, err := f.service.Link(ctx, tc.viewer(f), f.acme.ID, tc.repo(f), tc.branch, tc.number)
			if failed(t, "Link", err, tc.want) {
				if got, _ := f.store.ByID(ctx, first.ID); got.State != tc.state {
					t.Fatalf("the issue is %s, want %s", got.State, tc.state)
				}
				return
			}
			if link.LinkedBy != issues.LinkedByHand || link.Issue.Number != tc.number || link.Issue.State != tc.state {
				t.Fatalf("Link = %+v", link)
			}
			want := []string{"issue.created", "issue.branch_linked", "issue.changed"}
			if tc.number == done.Number {
				want = want[:2]
			}
			if got := kinds(f.store.Events[link.Issue.ID]); !slices.Equal(got, want) {
				t.Fatalf("events %v, want %v", got, want)
			}
		})
	}
}

func TestRelinkAndUnlink(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	first := f.create(t, f.olivia, issues.Input{Title: "Log in"})
	second := f.create(t, f.olivia, issues.Input{Title: "Sign up", State: issues.StateInReview})
	f.store.AddBranch(f.website.ID, "login")
	if _, err := f.service.Link(ctx, f.max, f.acme.ID, f.website.ID, "login", first.Number); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Link(ctx, f.max, f.acme.ID, f.website.ID, "login", first.Number); err != nil {
		t.Fatal(err)
	}
	if got := kinds(f.store.Events[first.ID]); !slices.Equal(got, []string{"issue.created", "issue.branch_linked", "issue.changed"}) {
		t.Fatalf("linking twice: events %v", got)
	}
	link, err := f.service.Link(ctx, f.max, f.acme.ID, f.website.ID, "login", second.Number)
	if err != nil {
		t.Fatal(err)
	}
	if link.Issue.State != issues.StateInReview {
		t.Fatalf("an issue in review moved to %s", link.Issue.State)
	}
	if got := kinds(f.store.Events[first.ID]); got[len(got)-1] != "issue.branch_unlinked" {
		t.Fatalf("the first issue's events %v, want the unlink last", got)
	}
	if err := f.service.Unlink(ctx, f.gwen, f.acme.ID, f.website.ID, "login"); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("a guest's Unlink = %v", err)
	}
	for range 2 {
		if err := f.service.Unlink(ctx, f.max, f.acme.ID, f.website.ID, "login"); err != nil {
			t.Fatal(err)
		}
	}
	if got := kinds(f.store.Events[second.ID]); !slices.Equal(got, []string{"issue.created", "issue.branch_linked", "issue.branch_unlinked"}) {
		t.Fatalf("the second issue's events %v", got)
	}
	got, err := f.service.OfBranch(ctx, f.max, f.acme.ID, f.website.ID, "login")
	if err != nil || got != nil {
		t.Fatalf("OfBranch after the unlink = %+v, %v", got, err)
	}
}

// shipFixture is fixture with issue ACME-1, which Olivia linked branches ship of website and of secret to, and
// branch other of website, unlinked.
func shipFixture(t *testing.T) (*fixture, issues.Issue) {
	t.Helper()
	f := newFixture(t)
	issue := f.create(t, f.olivia, issues.Input{Title: "Ship it"})
	f.store.AddBranch(f.website.ID, "ship")
	f.store.AddBranch(f.secret.ID, "ship")
	f.store.AddBranch(f.website.ID, "other")
	for _, repo := range []uuid.UUID{f.website.ID, f.secret.ID} {
		if _, err := f.service.Link(context.Background(), f.olivia, f.acme.ID, repo, "ship", issue.Number); err != nil {
			t.Fatal(err)
		}
	}
	return f, issue
}

func TestOfBranch(t *testing.T) {
	f, issue := shipFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		viewer func(f *fixture) issues.Viewer
		repo   func(f *fixture) uuid.UUID
		branch string
		linked bool
		want   error
	}{
		{"the owner", func(f *fixture) issues.Viewer { return f.olivia }, func(f *fixture) uuid.UUID { return f.secret.ID }, "ship", true, nil},
		{"a member", func(f *fixture) issues.Viewer { return f.max }, func(f *fixture) uuid.UUID { return f.website.ID }, "ship", true, nil},
		{"an unlinked branch", func(f *fixture) issues.Viewer { return f.max }, func(f *fixture) uuid.UUID { return f.website.ID }, "other", false, nil},
		{"a guest reads no issues", func(f *fixture) issues.Viewer { return f.gwen }, func(f *fixture) uuid.UUID { return f.website.ID }, "ship", false,
			nil},
		{"a repository the viewer doesn't read", func(f *fixture) issues.Viewer { return f.max }, func(f *fixture) uuid.UUID { return f.secret.ID },
			"ship", false, nil},
		{"no such branch", func(f *fixture) issues.Viewer { return f.max }, func(f *fixture) uuid.UUID { return f.website.ID }, "nope", false, nil},
		{"the default branch", func(f *fixture) issues.Viewer { return f.max }, func(f *fixture) uuid.UUID { return f.website.ID }, "main", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link, err := f.service.OfBranch(ctx, tc.viewer(f), f.acme.ID, tc.repo(f), tc.branch)
			if failed(t, "OfBranch", err, tc.want) {
				return
			}
			if (link != nil) != tc.linked || (link != nil && (link.Issue.ID != issue.ID || link.LinkedBy != issues.LinkedByHand)) {
				t.Fatalf("OfBranch = %+v, want linked %v", link, tc.linked)
			}
		})
	}
}

func TestBranches(t *testing.T) {
	f, issue := shipFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		viewer func(f *fixture) issues.Viewer
		want   []string
		err    error
	}{
		{"the owner", func(f *fixture) issues.Viewer { return f.olivia }, []string{"secret/ship", "website/ship"}, nil},
		{"a member sees the repositories they read", func(f *fixture) issues.Viewer { return f.max }, []string{"website/ship"}, nil},
		{"a guest reads no issues", func(f *fixture) issues.Viewer { return f.gwen }, nil, auth.ErrForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			branches, err := f.service.Branches(ctx, tc.viewer(f), f.acme.ID, issue.Number)
			if failed(t, "Branches", err, tc.err) {
				return
			}
			var got []string
			for _, b := range branches {
				got = append(got, b.Repository.Slug+"/"+b.Name)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("Branches = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLinkEventsHideRepositories(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	issue := f.create(t, f.olivia, issues.Input{Title: "Ship it"})
	f.store.AddBranch(f.secret.ID, "ship")
	if _, err := f.service.Link(ctx, f.olivia, f.acme.ID, f.secret.ID, "ship", issue.Number); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		viewer func(f *fixture) issues.Viewer
		branch string
	}{
		{"the owner", func(f *fixture) issues.Viewer { return f.olivia }, "ship"},
		{"a member who doesn't read the repository", func(f *fixture) issues.Viewer { return f.max }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events, err := f.service.Activity(ctx, tc.viewer(f), f.acme.ID, issue.Number)
			if err != nil {
				t.Fatal(err)
			}
			if b := events[1].Branch; b == nil || b.Name != tc.branch || b.RepositoryID != f.secret.ID {
				t.Fatalf("the link's branch = %+v, want %q", b, tc.branch)
			}
		})
	}
}

func kinds(events []issues.Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}
