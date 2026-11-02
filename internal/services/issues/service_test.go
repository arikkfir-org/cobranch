package issues_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code/codetest"
	"cobranch.dev/internal/services/issues"
	"cobranch.dev/internal/services/issues/issuestest"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/repos"
)

var now = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// fixture is Acme, key ACME, with Olivia its Owner, Max a Member and Gwen a Guest; Nia belongs nowhere. Acme has
// two repositories: website, which Max reads as its Developer, and secret, which only Olivia reads.
type fixture struct {
	orgs                   *orgstest.Store
	store                  *issuestest.Store
	service                *issues.Service
	acme                   orgs.Organization
	olivia, max, gwen, nia issues.Viewer
	website, secret        repos.Repository
}

func user(name string) issues.Viewer {
	return issues.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: name}}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	o := orgstest.NewStore()
	s := issuestest.NewStore(o)
	f := &fixture{orgs: o, store: s, service: issues.NewService(s, &codetest.Renderer{}, auth.NewAuthorizer(o))}
	f.service.SetClock(func() time.Time { return now })
	f.olivia, f.max, f.gwen, f.nia = user("Olivia"), user("Max"), user("Gwen"), user("Nia")
	for _, v := range []issues.Viewer{f.olivia, f.max, f.gwen, f.nia} {
		o.AddUser(v.ID, v.DisplayName)
	}
	f.acme = orgs.Organization{ID: uuid.New(), Slug: "acme", Name: "Acme", IssueKey: "ACME", CreatedAt: now}
	o.Orgs[f.acme.ID], o.Slugs[f.acme.Slug] = f.acme, f.acme.ID
	o.AddMember(f.acme.ID, f.olivia.ID, auth.RoleOwner)
	o.AddMember(f.acme.ID, f.max.ID, auth.RoleMember)
	o.AddMember(f.acme.ID, f.gwen.ID, auth.RoleGuest)
	f.website = repos.Repository{ID: uuid.New(), OrgID: f.acme.ID, Slug: "website", Name: "Website"}
	f.secret = repos.Repository{ID: uuid.New(), OrgID: f.acme.ID, Slug: "secret", Name: "Secret"}
	s.AddRepository(f.website)
	s.AddRepository(f.secret)
	o.Grant(f.max.ID, auth.RoleDeveloper, auth.ScopeRepository, f.website.ID)
	o.Grant(f.gwen.ID, auth.RoleReader, auth.ScopeRepository, f.website.ID)
	return f
}

func (f *fixture) create(t *testing.T, v issues.Viewer, in issues.Input) issues.Issue {
	t.Helper()
	issue, err := f.service.Create(context.Background(), v, f.acme.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	return issue
}

// failed reports whether call failed as want says it should, a validation error of any field for an
// *orgs.ValidationError, and fails t when it failed otherwise, or when it didn't fail but should have.
func failed(t *testing.T, call string, err, want error) bool {
	t.Helper()
	var verr *orgs.ValidationError
	switch {
	case want == nil && err != nil:
		t.Fatalf("%s = %v", call, err)
	case errors.As(want, &verr):
		if !errors.As(err, &verr) {
			t.Fatalf("%s = %v, want a validation error", call, err)
		}
	case want != nil && !errors.Is(err, want):
		t.Fatalf("%s = %v, want %v", call, err, want)
	}
	return want != nil
}

func TestCreate(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		viewer func(f *fixture) issues.Viewer
		in     func(f *fixture) issues.Input
		want   error
	}{
		{"a member", func(f *fixture) issues.Viewer { return f.max }, func(f *fixture) issues.Input {
			return issues.Input{Title: "  Show pushes ", Description: "**Now**", Priority: issues.PriorityHigh, AssigneeID: f.olivia.ID, RepositoryID: f.website.ID}
		}, nil},
		{"done from the start", func(f *fixture) issues.Viewer { return f.olivia }, func(*fixture) issues.Input {
			return issues.Input{Title: "Done", State: issues.StateDone}
		}, nil},
		{"a guest", func(f *fixture) issues.Viewer { return f.gwen }, func(*fixture) issues.Input { return issues.Input{Title: "x"} }, auth.ErrForbidden},
		{"no member", func(f *fixture) issues.Viewer { return f.nia }, func(*fixture) issues.Input { return issues.Input{Title: "x"} }, orgs.ErrNotFound},
		{"no title", func(f *fixture) issues.Viewer { return f.max }, func(*fixture) issues.Input { return issues.Input{Title: " "} }, &orgs.ValidationError{}},
		{"two lines", func(f *fixture) issues.Viewer { return f.max }, func(*fixture) issues.Input { return issues.Input{Title: "a\nb"} }, &orgs.ValidationError{}},
		{"an unknown state", func(f *fixture) issues.Viewer { return f.max }, func(*fixture) issues.Input {
			return issues.Input{Title: "x", State: "doing"}
		}, &orgs.ValidationError{}},
		{"an unknown priority", func(f *fixture) issues.Viewer { return f.max }, func(*fixture) issues.Input {
			return issues.Input{Title: "x", Priority: 5}
		}, &orgs.ValidationError{}},
		{"an assignee from outside", func(f *fixture) issues.Viewer { return f.max }, func(f *fixture) issues.Input {
			return issues.Input{Title: "x", AssigneeID: f.nia.ID}
		}, &orgs.ValidationError{}},
		{"a repository the viewer doesn't read", func(f *fixture) issues.Viewer { return f.max }, func(f *fixture) issues.Input {
			return issues.Input{Title: "x", RepositoryID: f.secret.ID}
		}, &orgs.ValidationError{}},
		{"another organization's repository", func(f *fixture) issues.Viewer { return f.olivia }, func(*fixture) issues.Input {
			return issues.Input{Title: "x", RepositoryID: uuid.New()}
		}, &orgs.ValidationError{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			got, err := f.service.Create(ctx, tc.viewer(f), f.acme.ID, tc.in(f))
			if failed(t, "Create()", err, tc.want) {
				return
			}
			if got.Number != 1 || got.Identifier() != "ACME-1" || got.CreatedBy == nil || got.CreatedBy.ID != tc.viewer(f).ID || got.Version != 1 {
				t.Errorf("Create() = %+v", got)
			}
			if got.State.Closed() != (got.ClosedAt != nil) {
				t.Errorf("Create() in %s closed at %v", got.State, got.ClosedAt)
			}
		})
	}
	f := newFixture(t)
	got := f.create(t, f.max, issues.Input{Title: "  Show pushes ", Description: "**Now**", AssigneeID: f.olivia.ID, RepositoryID: f.website.ID})
	if got.Title != "Show pushes" || got.State != issues.StateBacklog || got.Assignee == nil || got.Assignee.DisplayName != "Olivia" ||
		got.Repository == nil || got.Repository.Slug != "website" || got.DescriptionHTML != `<div data-doc="acme/@::issues/ACME-1">**Now**</div>` {
		t.Errorf("Create() = %+v", got)
	}
}

func TestGetHidesRepositories(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.create(t, f.olivia, issues.Input{Title: "Secret work", RepositoryID: f.secret.ID})
	if got, err := f.service.Get(ctx, f.olivia, f.acme.ID, 1); err != nil || got.Repository == nil || got.Repository.ID != f.secret.ID {
		t.Errorf("Olivia's Get() = %+v, %v; want it about secret", got, err)
	}
	if got, err := f.service.Get(ctx, f.max, f.acme.ID, 1); err != nil || got.Repository != nil {
		t.Errorf("Max's Get() = %+v, %v; want no repository, which he doesn't read", got, err)
	}
	if _, err := f.service.Get(ctx, f.max, f.acme.ID, 2); !errors.Is(err, issues.ErrNotFound) {
		t.Errorf("Get(2) = %v, want %v", err, issues.ErrNotFound)
	}
	if _, err := f.service.Get(ctx, f.gwen, f.acme.ID, 1); !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("a guest's Get() = %v, want %v", err, auth.ErrForbidden)
	}
	// Filtering by a repository Max doesn't read finds nothing, as if no issue were about it.
	secret := f.secret.ID
	if page, err := f.service.List(ctx, f.max, f.acme.ID, issues.Filter{RepositoryID: &secret}); err != nil || len(page.Issues) != 0 {
		t.Errorf("Max's List(secret) = %+v, %v; want none", page, err)
	}
}

func TestList(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, title := range []string{"One", "Two", "Three"} {
		f.create(t, f.max, issues.Input{Title: title})
	}
	f.create(t, f.max, issues.Input{Title: "Four", State: issues.StateCanceled})
	page, err := f.service.List(ctx, f.max, f.acme.ID, issues.Filter{Limit: 2})
	if err != nil || len(page.Issues) != 2 || page.Issues[0].Number != 3 || page.Next == "" {
		t.Fatalf("List(limit 2) = %+v, %v", page, err)
	}
	rest, err := f.service.List(ctx, f.max, f.acme.ID, issues.Filter{Limit: 2, Cursor: page.Next})
	if err != nil || len(rest.Issues) != 1 || rest.Issues[0].Number != 1 || rest.Next != "" {
		t.Errorf("List(next) = %+v, %v; want ACME-1 alone", rest, err)
	}
	all, err := f.service.List(ctx, f.max, f.acme.ID, issues.Filter{States: issues.States})
	if err != nil || len(all.Issues) != 4 {
		t.Errorf("List(every state) = %d, %v; want 4", len(all.Issues), err)
	}
	byID, err := f.service.List(ctx, f.max, f.acme.ID, issues.Filter{States: issues.States, Text: "acme-4"})
	if err != nil || len(byID.Issues) != 1 || byID.Issues[0].Number != 4 {
		t.Errorf("List(acme-4) = %+v, %v; want ACME-4", byID, err)
	}
	for name, filter := range map[string]issues.Filter{
		"a cursor that isn't": {Cursor: "nope"}, "a state that isn't": {States: []issues.State{"doing"}}, "too many": {Limit: 101},
	} {
		var verr *orgs.ValidationError
		if _, err := f.service.List(ctx, f.max, f.acme.ID, filter); !errors.As(err, &verr) {
			t.Errorf("%s: List() = %v, want a validation error", name, err)
		}
	}
	if _, err := f.service.List(ctx, f.nia, f.acme.ID, issues.Filter{}); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("an outsider's List() = %v, want %v", err, orgs.ErrNotFound)
	}
}

func TestUpdate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.create(t, f.max, issues.Input{Title: "Show pushes", AssigneeID: f.max.ID})
	state, title, nobody, website := issues.StateDone, "Show every push", uuid.Nil, f.website.ID
	got, err := f.service.Update(ctx, f.max, f.acme.ID, 1, issues.Patch{Title: &title, State: &state, AssigneeID: &nobody, RepositoryID: &website, Version: 1})
	if err != nil || got.Title != title || got.State != issues.StateDone || got.ClosedAt == nil || !got.ClosedAt.Equal(now) || got.Assignee != nil ||
		got.Repository == nil || got.Version != 2 {
		t.Fatalf("Update() = %+v, %v", got, err)
	}
	activity, err := f.service.Activity(ctx, f.max, f.acme.ID, 1)
	if err != nil || len(activity) != 2 {
		t.Fatalf("Activity() = %+v, %v", activity, err)
	}
	var fields []string
	for _, c := range activity[1].Changes {
		fields = append(fields, c.Field)
	}
	if !slices.Equal(fields, []string{"title", "state", "assignee", "repository"}) || activity[1].Changes[1].From != "backlog" ||
		activity[1].Changes[2].From != "Max" || activity[1].Changes[3].To != "website" {
		t.Errorf("Activity()'s changes = %+v", activity[1].Changes)
	}

	if _, err := f.service.Update(ctx, f.max, f.acme.ID, 1, issues.Patch{Title: &title, Version: 1}); !errors.Is(err, issues.ErrChanged) {
		t.Errorf("Update() from version 1 again = %v, want %v", err, issues.ErrChanged)
	}
	same, err := f.service.Update(ctx, f.max, f.acme.ID, 1, issues.Patch{Title: &title})
	if err != nil || same.Version != 2 {
		t.Errorf("Update() that changes nothing = %+v, %v; want version 2 still", same, err)
	}
	canceled := issues.StateCanceled
	if again, err := f.service.Update(ctx, f.max, f.acme.ID, 1, issues.Patch{State: &canceled}); err != nil || again.ClosedAt == nil {
		t.Errorf("Update(canceled) = %+v, %v; want it closed still", again, err)
	}
	reopen := issues.StateTodo
	if open, err := f.service.Update(ctx, f.max, f.acme.ID, 1, issues.Patch{State: &reopen}); err != nil || open.ClosedAt != nil {
		t.Errorf("Update(todo) = %+v, %v; want it open", open, err)
	}
	secret := f.secret.ID
	var verr *orgs.ValidationError
	if _, err := f.service.Update(ctx, f.max, f.acme.ID, 1, issues.Patch{RepositoryID: &secret}); !errors.As(err, &verr) {
		t.Errorf("Update() to a repository Max doesn't read = %v, want a validation error", err)
	}
	if _, err := f.service.Update(ctx, f.gwen, f.acme.ID, 1, issues.Patch{Title: &title}); !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("a guest's Update() = %v, want %v", err, auth.ErrForbidden)
	}
}

func TestActivityHidesRepositories(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.create(t, f.olivia, issues.Input{Title: "Secret work", RepositoryID: f.secret.ID})
	website := f.website.ID
	if _, err := f.service.Update(ctx, f.olivia, f.acme.ID, 1, issues.Patch{RepositoryID: &website}); err != nil {
		t.Fatal(err)
	}
	activity, err := f.service.Activity(ctx, f.max, f.acme.ID, 1)
	if err != nil || len(activity) != 2 || activity[1].Changes[0].From != "" || activity[1].Changes[0].To != "website" {
		t.Errorf("Max's Activity() = %+v, %v; want the move from a repository he doesn't read to website", activity, err)
	}
}

func TestDelete(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.create(t, f.max, issues.Input{Title: "A mistake"})
	if err := f.service.Delete(ctx, f.max, f.acme.ID, 1, 1); !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("a member's Delete() = %v, want %v", err, auth.ErrForbidden)
	}
	if err := f.service.Delete(ctx, f.olivia, f.acme.ID, 1, 2); !errors.Is(err, issues.ErrChanged) {
		t.Errorf("Delete() at version 2 = %v, want %v", err, issues.ErrChanged)
	}
	if err := f.service.Delete(ctx, f.olivia, f.acme.ID, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Get(ctx, f.olivia, f.acme.ID, 1); !errors.Is(err, issues.ErrNotFound) {
		t.Errorf("Get() after Delete() = %v, want %v", err, issues.ErrNotFound)
	}
}

func TestParseIdentifier(t *testing.T) {
	for in, want := range map[string]int{"ACME-42": 42, "acme-42": 42, "42": 42, " 7 ": 7, "ACME-0": 0, "ACME-": 0, "-4": 0, "a b-4": 0, "x": 0, "+4": 0} {
		got, ok := issues.ParseIdentifier(in)
		if got != want || ok != (want != 0) {
			t.Errorf("ParseIdentifier(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
}

func TestPriority(t *testing.T) {
	for _, p := range []issues.Priority{issues.PriorityNone, issues.PriorityUrgent, issues.PriorityHigh, issues.PriorityMedium, issues.PriorityLow} {
		if got, err := issues.ParsePriority(p.String()); err != nil || got != p {
			t.Errorf("ParsePriority(%q) = %v, %v", p.String(), got, err)
		}
	}
	if _, err := issues.ParsePriority("asap"); err == nil {
		t.Error("ParsePriority(asap) took it")
	}
}

func TestIssueKey(t *testing.T) {
	for key, ok := range map[string]bool{"ACME": true, "A": true, "A1B2C3D4E5": true, "A1B2C3D4E5F": false, "1A": false, "acme": false, "": false, "AC-ME": false} {
		if err := orgs.ValidateIssueKey(key); (err == nil) != ok {
			t.Errorf("ValidateIssueKey(%q) = %v", key, err)
		}
	}
	for slug, want := range map[string]string{"acme": "ACME", "acme-corp": "ACME", "globex-corp": "GLOBE", "2fa": "FA", "42": "ISSUE"} {
		if got := orgs.DefaultIssueKey(slug); got != want {
			t.Errorf("DefaultIssueKey(%q) = %q, want %q", slug, got, want)
		}
	}
}
