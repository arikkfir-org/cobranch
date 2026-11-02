package postgres

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/issues"
	"cobranch.dev/internal/services/orgs"
)

type issueFixture struct {
	*repoFixture
	is *IssueStore
}

func newIssueFixture(t *testing.T) *issueFixture {
	t.Helper()
	f := &issueFixture{repoFixture: newRepoFixture(t)}
	f.is = NewIssueStore(f.pool)
	return f
}

// newIssue is an issue of Acme's titled title, created by Olivia.
func (f *issueFixture) newIssue(title string) issues.Issue {
	return issues.Issue{ID: uuid.Must(uuid.NewV7()), OrgID: f.acme.ID, Title: title, State: issues.StateBacklog,
		CreatedBy: &issues.Principal{ID: f.olivia}, CreatedAt: now, UpdatedAt: now, Version: 1}
}

func (f *issueFixture) create(t *testing.T, issue issues.Issue) issues.Issue {
	t.Helper()
	created, err := f.is.Create(context.Background(), f.olivia, issue)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func TestIssueKeys(t *testing.T) {
	t.Parallel()
	f := newOrgFixture(t)
	ctx := context.Background()
	org, err := f.s.ByID(ctx, f.acme.ID)
	if err != nil || org.IssueKey != "ACME" {
		t.Fatalf("ByID() = %+v, %v; want key ACME, the slug's default", org, err)
	}
	// An organization the database makes without a key, as the seeds do, takes its slug's default too.
	for slug, want := range map[string]string{"globex-corp": "GLOBE", "2fa-co": "FA", "42": "ISSUE"} {
		var key string
		if err := f.pool.QueryRow(ctx, "INSERT INTO organizations (id, slug, name, membership_role_id) VALUES ($1, $2, 'x', $3) RETURNING issue_key",
			uuid.New(), slug, auth.RoleMember).Scan(&key); err != nil || key != want || orgs.DefaultIssueKey(slug) != want {
			t.Errorf("slug %s: key %q, %v; DefaultIssueKey() = %q; want %q", slug, key, err, orgs.DefaultIssueKey(slug), want)
		}
	}
	org.IssueKey = "ACM2"
	if err := f.s.Update(ctx, f.olivia, org); err != nil {
		t.Fatal(err)
	}
	if got, err := f.s.ByID(ctx, f.acme.ID); err != nil || got.IssueKey != "ACM2" {
		t.Errorf("ByID() after Update() = %+v, %v; want key ACM2", got, err)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE organizations SET issue_key = 'acme' WHERE id = $1", f.acme.ID); err == nil {
		t.Error("a lowercase issue key was kept")
	}
}

func TestIssueStoreCreate(t *testing.T) {
	t.Parallel()
	f := newIssueFixture(t)
	ctx := context.Background()
	first := f.newIssue("Show pushes")
	first.Assignee, first.Repository = &issues.Principal{ID: f.max}, &issues.Repository{ID: f.website.ID}
	got := f.create(t, first)
	if got.Number != 1 || got.Key != "ACME" || got.OrgSlug != "acme" || got.Identifier() != "ACME-1" || got.Assignee == nil ||
		got.Assignee.DisplayName == "" || got.Repository == nil || got.Repository.Slug != "website" || got.CreatedBy == nil ||
		got.CreatedBy.ID != f.olivia || got.Version != 1 || got.State != issues.StateBacklog {
		t.Fatalf("Create() = %+v", got)
	}
	// Issues created at once take numbers one after another.
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Go(func() {
			_, err := f.is.Create(ctx, f.olivia, f.newIssue("At once"))
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	page, more, err := f.is.List(ctx, f.acme.ID, issues.Query{States: issues.States, Limit: 10})
	if err != nil || more || len(page) != 6 {
		t.Fatalf("List() = %d issues, %v, %v", len(page), more, err)
	}
	var numbers []int
	for _, i := range page {
		numbers = append(numbers, i.Number)
	}
	if !slices.Equal(numbers, []int{6, 5, 4, 3, 2, 1}) {
		t.Errorf("numbers = %v, want 6 to 1", numbers)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = 'issue.created' AND org_id = $1 AND actor_id = $2", f.acme.ID, f.olivia); n != 6 {
		t.Errorf("%d issue.created events, want 6", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'issues' AND principal_id = $1", f.olivia); n != 6 {
		t.Errorf("%d audit rows of issues by Olivia, want 6", n)
	}
	if _, err := f.is.Get(ctx, f.acme.ID, 7); !errors.Is(err, issues.ErrNotFound) {
		t.Errorf("Get(7) = %v, want %v", err, issues.ErrNotFound)
	}
	if _, err := f.is.Get(ctx, uuid.New(), 1); !errors.Is(err, issues.ErrNotFound) {
		t.Errorf("another organization's Get(1) = %v, want %v", err, issues.ErrNotFound)
	}
}

func TestIssueStoreList(t *testing.T) {
	t.Parallel()
	f := newIssueFixture(t)
	ctx := context.Background()
	login := f.newIssue("Fix the 100% login")
	login.Assignee = &issues.Principal{ID: f.max}
	f.create(t, login)
	docs := f.newIssue("Write docs")
	docs.Repository, docs.Description = &issues.Repository{ID: f.website.ID}, "About the login page"
	f.create(t, docs)
	done := f.newIssue("Done already")
	done.State, done.ClosedAt = issues.StateDone, &now
	f.create(t, done)

	nobody, maxID, website := uuid.Nil, f.max, f.website.ID
	for _, tc := range []struct {
		name string
		q    issues.Query
		want []int
	}{
		{"open", issues.Query{States: issues.OpenStates}, []int{2, 1}},
		{"closed", issues.Query{States: []issues.State{issues.StateDone, issues.StateCanceled}}, []int{3}},
		{"Max's", issues.Query{States: issues.States, AssigneeID: &maxID}, []int{1}},
		{"nobody's", issues.Query{States: issues.States, AssigneeID: &nobody}, []int{3, 2}},
		{"website's", issues.Query{States: issues.States, RepositoryID: &website}, []int{2}},
		{"no repository's", issues.Query{States: issues.States, RepositoryID: &nobody}, []int{3, 1}},
		{"text in title or description", issues.Query{States: issues.States, Text: "LOGIN"}, []int{2, 1}},
		{"a literal percent", issues.Query{States: issues.States, Text: "100%"}, []int{1}},
		{"a wildcard that isn't", issues.Query{States: issues.States, Text: "_"}, nil},
		{"by number", issues.Query{States: issues.States, Text: "ACME-3", Number: 3}, []int{3}},
		{"below 3", issues.Query{States: issues.States, Below: 3}, []int{2, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.q.Limit = 10
			got, _, err := f.is.List(ctx, f.acme.ID, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			var numbers []int
			for _, i := range got {
				numbers = append(numbers, i.Number)
			}
			if !slices.Equal(numbers, tc.want) {
				t.Errorf("numbers = %v, want %v", numbers, tc.want)
			}
		})
	}
	page, more, err := f.is.List(ctx, f.acme.ID, issues.Query{States: issues.States, Limit: 2})
	if err != nil || !more || len(page) != 2 {
		t.Errorf("List(limit 2) = %d, %v, %v; want 2 and more", len(page), more, err)
	}
}

func TestIssueStoreUpdate(t *testing.T) {
	t.Parallel()
	f := newIssueFixture(t)
	ctx := context.Background()
	issue := f.create(t, f.newIssue("Show pushes"))
	next := issue
	next.Title, next.State, next.ClosedAt = "Show every push", issues.StateDone, &now
	next.Assignee = &issues.Principal{ID: f.max, DisplayName: "Max"}
	changes := []issues.Change{{Field: "title", From: issue.Title, To: next.Title}, {Field: "assignee", To: "Max", ToID: f.max}}
	updated, err := f.is.Update(ctx, f.max, next, issue.Version, changes)
	if err != nil || updated.Version != 2 || updated.Title != "Show every push" {
		t.Fatalf("Update() = %+v, %v", updated, err)
	}
	got, err := f.is.Get(ctx, f.acme.ID, 1)
	if err != nil || got.Title != "Show every push" || got.State != issues.StateDone || got.ClosedAt == nil || got.Assignee == nil ||
		got.Assignee.ID != f.max || got.Version != 2 {
		t.Errorf("Get() after Update() = %+v, %v", got, err)
	}
	if _, err := f.is.Update(ctx, f.max, next, issue.Version, changes); !errors.Is(err, issues.ErrChanged) {
		t.Errorf("Update() from version 1 again = %v, want %v", err, issues.ErrChanged)
	}
	activity, err := f.is.Activity(ctx, issue.ID)
	if err != nil || len(activity) != 2 || activity[0].Kind != "issue.created" || activity[1].Kind != "issue.changed" ||
		activity[1].Actor == nil || activity[1].Actor.ID != f.max || len(activity[1].Changes) != 2 || activity[1].Changes[1].ToID != f.max ||
		activity[1].Changes[0].From != "Show pushes" {
		t.Errorf("Activity() = %+v, %v", activity, err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'issues' AND principal_id = $1", f.max); n != 1 {
		t.Errorf("%d audit rows of issues by Max, want 1", n)
	}
	// A closed issue keeps when it closed.
	if _, err := f.pool.Exec(ctx, "UPDATE issues SET closed_at = NULL WHERE id = $1", issue.ID); err == nil {
		t.Error("a Done issue without closed_at was kept")
	}
}

func TestIssueStoreDelete(t *testing.T) {
	t.Parallel()
	f := newIssueFixture(t)
	ctx := context.Background()
	issue := f.create(t, f.newIssue("A mistake"))
	if err := f.is.Delete(ctx, f.olivia, issue, 2); !errors.Is(err, issues.ErrChanged) {
		t.Errorf("Delete() at version 2 = %v, want %v", err, issues.ErrChanged)
	}
	if err := f.is.Delete(ctx, f.olivia, issue, 1); err != nil {
		t.Fatal(err)
	}
	if err := f.is.Delete(ctx, f.olivia, issue, 1); !errors.Is(err, issues.ErrNotFound) {
		t.Errorf("Delete() again = %v, want %v", err, issues.ErrNotFound)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE subject_id = $1", issue.ID); n != 0 {
		t.Errorf("%d events of the deleted issue, want 0", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = 'issue.deleted' AND subject_id = $1 AND details->>'issue' = $2", f.acme.ID, issue.ID.String()); n != 1 {
		t.Errorf("%d issue.deleted events, want 1", n)
	}
	// A deleted issue's number is never given again.
	if next := f.create(t, f.newIssue("Next")); next.Number != 2 {
		t.Errorf("the next issue's number = %d, want 2", next.Number)
	}
}

func TestIssueStoreMemberAndRepository(t *testing.T) {
	t.Parallel()
	f := newIssueFixture(t)
	ctx := context.Background()
	if p, err := f.is.Member(ctx, f.acme.ID, f.max); err != nil || p.ID != f.max || p.DisplayName == "" {
		t.Errorf("Member(Max) = %+v, %v", p, err)
	}
	if _, err := f.is.Member(ctx, f.acme.ID, f.nia); !errors.Is(err, issues.ErrNotFound) {
		t.Errorf("Member(Nia), who isn't one, = %v, want %v", err, issues.ErrNotFound)
	}
	if r, err := f.is.Repository(ctx, f.acme.ID, f.website.ID); err != nil || r.Slug != "website" {
		t.Errorf("Repository(website) = %+v, %v", r, err)
	}
	if _, err := f.is.Repository(ctx, uuid.New(), f.website.ID); !errors.Is(err, issues.ErrNotFound) {
		t.Errorf("another organization's Repository() = %v, want %v", err, issues.ErrNotFound)
	}
}
