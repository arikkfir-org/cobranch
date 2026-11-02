package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/issues"
)

// issueTaskFixture is agentFixture with the issues' store.
type issueTaskFixture struct {
	*agentFixture
	is *IssueStore
}

func newIssueTaskFixture(t *testing.T) *issueTaskFixture {
	t.Helper()
	f := &issueTaskFixture{agentFixture: newAgentFixture(t)}
	f.is = NewIssueStore(f.pool)
	return f
}

// handOver records issue as write answers it, then gives Scout its task, in one of the issues store's transactions.
func (f *issueTaskFixture) handOver(ctx context.Context, write func(ctx context.Context) (issues.Issue, error), kind string) (issues.Issue, error) {
	var out issues.Issue
	err := f.is.Transact(ctx, f.max, func(ctx context.Context) error {
		var err error
		if out, err = write(ctx); err != nil {
			return err
		}
		_, err = f.a.Assign(ctx, f.max, agents.Task{ID: v7(), OrgID: f.acme.ID, Agent: agents.Principal{ID: f.scout.ID}, Kind: kind,
			AssignedBy: &agents.Principal{ID: f.max}, Title: "ACME-1 Fix", RepoID: f.website.ID, IssueID: out.ID})
		return err
	})
	return out, err
}

func TestIssueStoreAgentAssignee(t *testing.T) {
	t.Parallel()
	f := newIssueTaskFixture(t)
	ctx := context.Background()
	if p, err := f.is.Assignee(ctx, f.acme.ID, f.scout.ID); err != nil || !p.Agent || p.DisplayName != "Scout" {
		t.Errorf("Assignee(Scout) = %+v, %v; want the agent", p, err)
	}
	if _, err := f.is.Assignee(ctx, uuid.New(), f.scout.ID); !errors.Is(err, issues.ErrNotFound) {
		t.Errorf("Assignee(Scout) in another organization = %v, want %v", err, issues.ErrNotFound)
	}
}

func TestIssueStoreAgentTask(t *testing.T) {
	t.Parallel()
	f := newIssueTaskFixture(t)
	ctx := context.Background()
	issue := issues.Issue{ID: v7(), OrgID: f.acme.ID, Title: "Fix", State: issues.StateTodo, Assignee: &issues.Principal{ID: f.scout.ID},
		CreatedBy: &issues.Principal{ID: f.max}, CreatedAt: now, UpdatedAt: now, Version: 1}
	if _, err := f.handOver(ctx, func(ctx context.Context) (issues.Issue, error) { return f.is.Create(ctx, f.max, issue) }, issues.KindTask); err != nil {
		t.Fatal(err)
	}
	got, err := f.is.Get(ctx, f.acme.ID, 1)
	if err != nil || got.Assignee == nil || !got.Assignee.Agent || got.Task == nil || *got.Task != (issues.Task{Number: 1, State: agents.TaskQueued, RepoID: f.website.ID}) {
		t.Fatalf("Get() = assignee %+v, task %+v, %v; want Scout and task 1", got.Assignee, got.Task, err)
	}
	if task, err := f.a.Task(ctx, f.acme.ID, 1); err != nil || task.IssueID != issue.ID || task.Kind != issues.KindTask {
		t.Errorf("the task = %+v, %v; want of kind issue, naming the issue", task, err)
	}
	if list, _, err := f.is.List(ctx, f.acme.ID, issues.Query{States: issues.States, Limit: 10}); err != nil || len(list) != 1 || list[0].Task == nil {
		t.Errorf("List() = %+v, %v; want the issue with its task", list, err)
	}

	// A task its kind refuses rolls the change of the issue back with it.
	next := got
	next.Title = "Fix it all"
	_, err = f.handOver(ctx, func(ctx context.Context) (issues.Issue, error) {
		return f.is.Update(ctx, f.max, next, got.Version, []issues.Change{{Field: "title", From: got.Title, To: next.Title}})
	}, "nonexistent")
	if err == nil {
		t.Fatal("a task of no kind was recorded")
	}
	if read, err := f.is.Get(ctx, f.acme.ID, 1); err != nil || read.Title != "Fix" || read.Version != got.Version {
		t.Errorf("Get() after a refused task = %q at %d, %v; want it as it was", read.Title, read.Version, err)
	}

	// Another assignee shows no task; the issue's deletion leaves its task, naming no issue.
	next = got
	next.Assignee = &issues.Principal{ID: f.max}
	if _, err := f.is.Update(ctx, f.max, next, got.Version, []issues.Change{{Field: "assignee", ToID: f.max}}); err != nil {
		t.Fatal(err)
	}
	read, err := f.is.Get(ctx, f.acme.ID, 1)
	if err != nil || read.Task != nil {
		t.Fatalf("Get() assigned to Max = task %+v, %v; want none", read.Task, err)
	}
	if err := f.is.Delete(ctx, f.olivia, read, read.Version); err != nil {
		t.Fatal(err)
	}
	if task, err := f.a.Task(ctx, f.acme.ID, 1); err != nil || task.IssueID != uuid.Nil {
		t.Errorf("the deleted issue's task = %+v, %v; want it, naming no issue", task, err)
	}
}

func TestIssueTasksMigration(t *testing.T) {
	t.Parallel()
	f := newIssueTaskFixture(t)
	ctx := context.Background()
	issue := issues.Issue{ID: v7(), OrgID: f.acme.ID, Title: "Fix", State: issues.StateTodo, Assignee: &issues.Principal{ID: f.scout.ID},
		CreatedBy: &issues.Principal{ID: f.max}, CreatedAt: now, UpdatedAt: now, Version: 1}
	if _, err := f.handOver(ctx, func(ctx context.Context) (issues.Issue, error) { return f.is.Create(ctx, f.max, issue) }, issues.KindTask); err != nil {
		t.Fatal(err)
	}
	f.assigned(t, "Look around", false, uuid.Nil)
	// Down past the migration forgets the issues' tasks and keeps the others; up again takes them anew.
	schema := newSchema(t, f.pool, migrations.FS, migrations.Go...)
	if _, err := schema.provider.DownTo(ctx, 35); err != nil {
		t.Fatal(err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM agent_tasks"); n != 1 {
		t.Errorf("%d tasks after rolling back, want the one that names no issue", n)
	}
	if _, err := schema.provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.handOver(ctx, func(ctx context.Context) (issues.Issue, error) { return f.is.Get(ctx, f.acme.ID, 1) }, issues.KindTask); err != nil {
		t.Errorf("a task of an issue after the migration ran again: %v", err)
	}
}
