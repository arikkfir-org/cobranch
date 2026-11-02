package issues_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/agents/agentstest"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/issues"
	"cobranch.dev/internal/services/issues/issuestest"
	"cobranch.dev/internal/services/orgs"
)

// agentFixture is the issues fixture with an agent, Builder, which the service hands issues to, and Tara, a member
// whose role writes issues but assigns no agents.
type agentFixture struct {
	*fixture
	tasks   *agentstest.Store
	builder agents.Agent
	tara    issues.Viewer
}

func newAgentFixture(t *testing.T) *agentFixture {
	t.Helper()
	f := &agentFixture{fixture: newFixture(t), tasks: agentstest.NewStore()}
	f.builder = f.tasks.AddAgent(agents.Agent{OrgID: f.acme.ID, Name: "Builder"})
	f.orgs.AddUser(f.builder.ID, "Builder")
	f.store.Agents[f.builder.ID] = f.acme.ID
	f.tasks.Repos[f.website.ID] = agentstest.Repository{Org: f.acme.ID, Slug: "website"}
	f.tasks.Repos[f.secret.ID] = agentstest.Repository{Org: f.acme.ID, Slug: "secret"}
	for _, v := range []issues.Viewer{f.olivia, f.max} {
		f.tasks.Names[v.ID] = v.DisplayName
	}
	tracker := orgs.Role{ID: uuid.New(), OrgID: f.acme.ID, Name: "Tracker", Permissions: []string{"org.read", "issues.read", "issues.write"}}
	f.orgs.RoleList = append(f.orgs.RoleList, tracker)
	f.tara = user("Tara")
	f.orgs.AddUser(f.tara.ID, "Tara")
	f.orgs.AddMember(f.acme.ID, f.tara.ID, tracker.ID)
	f.service.WithAgents(agents.NewService(f.tasks, auth.NewAuthorizer(f.orgs)), f.tasks)
	f.store.TaskOf = issuestest.TasksOf(f.tasks.KeptTasks)
	return f
}

// task is the organization's task of number, as the agents' store keeps it.
func (f *agentFixture) task(t *testing.T, number int64) agents.Task {
	t.Helper()
	task, err := f.tasks.Task(context.Background(), f.acme.ID, number)
	if err != nil {
		t.Fatalf("task %d: %v", number, err)
	}
	return task
}

func TestCreateAssignedToAnAgent(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	got := f.create(t, f.max, issues.Input{Title: "Fix the login", Description: "It **fails**.", AssigneeID: f.builder.ID, RepositoryID: f.website.ID})
	if got.Assignee == nil || !got.Assignee.Agent || got.Task == nil || got.Task.Number != 1 || got.Task.State != agents.TaskQueued {
		t.Fatalf("Create = assignee %+v, task %+v; want Builder and task 1, queued", got.Assignee, got.Task)
	}
	task := f.task(t, 1)
	want := agents.Task{Kind: issues.KindTask, IssueID: got.ID, Title: "ACME-1 Fix the login", Instructions: "It **fails**.", RepoID: f.website.ID}
	if task.Kind != want.Kind || task.IssueID != want.IssueID || task.Title != want.Title || task.Instructions != want.Instructions ||
		task.RepoID != want.RepoID || task.Agent.ID != f.builder.ID || task.AssignedBy == nil || task.AssignedBy.ID != f.max.ID {
		t.Errorf("task = %+v; want %+v by Max", task, want)
	}
	read, err := f.service.Get(context.Background(), f.max, f.acme.ID, got.Number)
	if err != nil || read.Task == nil || read.Task.Number != 1 {
		t.Errorf("Get = %+v, %v; want task 1", read.Task, err)
	}
	read, err = f.service.Get(context.Background(), f.olivia, f.acme.ID, got.Number)
	if err != nil || read.Task == nil {
		t.Errorf("Get by the Owner = %+v, %v; want task 1", read.Task, err)
	}
}

// leftAsItWas fails t unless the issue of number is assigned to nobody and no agent has a task.
func (f *agentFixture) leftAsItWas(t *testing.T, number int) {
	t.Helper()
	if _, err := f.tasks.Task(context.Background(), f.acme.ID, 1); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("a refused assignment left a task: %v", err)
	}
	if read, _ := f.service.Get(context.Background(), f.olivia, f.acme.ID, number); read.Assignee != nil {
		t.Errorf("a refused assignment assigned %+v", read.Assignee)
	}
}

func TestAssignAnAgent(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", agents.MaxInstructions+10)
	tests := []struct {
		name string
		v    func(*agentFixture) issues.Viewer
		in   func(*agentFixture) issues.Input
		want error
		// title and instructions are the task's, when it gets one.
		title, instructions string
	}{
		{name: "a member", v: func(f *agentFixture) issues.Viewer { return f.max },
			in: func(*agentFixture) issues.Input { return issues.Input{Title: "Fix"} }, title: "ACME-1 Fix"},
		{name: "a long title and description", v: func(f *agentFixture) issues.Viewer { return f.max },
			in: func(*agentFixture) issues.Input {
				return issues.Input{Title: strings.Repeat("t", issues.MaxTitle), Description: long}
			},
			title: "ACME-1 " + strings.Repeat("t", agents.MaxTitle-len("ACME-1 ")), instructions: "…the description goes on: read the issue for the rest."},
		{name: "without agents.assign", v: func(f *agentFixture) issues.Viewer { return f.tara },
			in: func(*agentFixture) issues.Input { return issues.Input{Title: "Fix"} }, want: auth.ErrForbidden},
		{name: "a repository the assigner doesn't read", v: func(f *agentFixture) issues.Viewer { return f.max },
			in: func(f *agentFixture) issues.Input { return issues.Input{Title: "Fix", RepositoryID: f.secret.ID} }, want: &orgs.ValidationError{}},
		{name: "the Owner, in any repository", v: func(f *agentFixture) issues.Viewer { return f.olivia },
			in: func(f *agentFixture) issues.Input { return issues.Input{Title: "Fix", RepositoryID: f.secret.ID} }, title: "ACME-1 Fix"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newAgentFixture(t)
			created := f.create(t, f.olivia, tt.in(f))
			id := f.builder.ID
			got, err := f.service.Update(context.Background(), tt.v(f), f.acme.ID, created.Number, issues.Patch{AssigneeID: &id})
			if failed(t, "Update", err, tt.want) {
				f.leftAsItWas(t, created.Number)
				return
			}
			if got.Task == nil || got.Task.Number != 1 {
				t.Fatalf("Update task = %+v; want task 1", got.Task)
			}
			task := f.task(t, 1)
			if task.Title != tt.title || (tt.instructions != "" && !strings.HasSuffix(task.Instructions, tt.instructions)) ||
				len([]rune(task.Instructions)) > agents.MaxInstructions {
				t.Errorf("task = %q, %d characters of instructions; want %q ending %q", task.Title, len([]rune(task.Instructions)), tt.title, tt.instructions)
			}
		})
	}
}

func TestReassignFromAnAgent(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	created := f.create(t, f.max, issues.Input{Title: "Fix", AssigneeID: f.builder.ID, RepositoryID: f.website.ID})
	olivia := f.olivia.ID
	got, err := f.service.Update(context.Background(), f.max, f.acme.ID, created.Number, issues.Patch{AssigneeID: &olivia})
	if err != nil || got.Task != nil {
		t.Fatalf("Update = task %+v, %v; want none", got.Task, err)
	}
	if task := f.task(t, 1); task.State != agents.TaskCancelled || task.CancelledBy == nil || task.CancelledBy.ID != f.max.ID {
		t.Errorf("the agent's task = %s by %+v; want cancelled by Max", task.State, task.CancelledBy)
	}
	builder := f.builder.ID
	if got, err = f.service.Update(context.Background(), f.max, f.acme.ID, created.Number, issues.Patch{AssigneeID: &builder}); err != nil || got.Task == nil || got.Task.Number != 2 {
		t.Fatalf("assigning it again = task %+v, %v; want task 2", got.Task, err)
	}
	f.tasks.SetState(f.acme.ID, 2, agents.TaskSucceeded)
	if got, err = f.service.Update(context.Background(), f.max, f.acme.ID, created.Number, issues.Patch{AssigneeID: &uuid.Nil}); err != nil || got.Assignee != nil {
		t.Fatalf("unassigning it after its task ended = %+v, %v", got.Assignee, err)
	}
	if task := f.task(t, 2); task.State != agents.TaskSucceeded {
		t.Errorf("an ended task = %s; want it as it ended", task.State)
	}
	done := issues.StateDone
	if _, err = f.service.Update(context.Background(), f.max, f.acme.ID, created.Number, issues.Patch{AssigneeID: &builder, State: &done}); err != nil {
		t.Fatal(err)
	}
	if task := f.task(t, 3); task.State != agents.TaskQueued {
		t.Errorf("closing the issue = task %s; want it left to run", task.State)
	}
}

func TestDeleteAnAgentsIssue(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	created := f.create(t, f.max, issues.Input{Title: "Fix", AssigneeID: f.builder.ID})
	if err := f.service.Delete(context.Background(), f.olivia, f.acme.ID, created.Number, created.Version); err != nil {
		t.Fatal(err)
	}
	if task := f.task(t, 1); task.State != agents.TaskCancelled {
		t.Errorf("the deleted issue's task = %s; want cancelled", task.State)
	}
}

func TestAgentTaskHidden(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	created := f.create(t, f.olivia, issues.Input{Title: "Secret work", AssigneeID: f.builder.ID, RepositoryID: f.secret.ID})
	if created.Task == nil {
		t.Fatal("Create gave the agent no task")
	}
	read, err := f.service.Get(context.Background(), f.max, f.acme.ID, created.Number)
	if err != nil || read.Task != nil || read.Assignee == nil {
		t.Errorf("Get by a member who doesn't read the repository = %+v, task %+v, %v; want the assignee alone", read.Assignee, read.Task, err)
	}
}
