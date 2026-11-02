package postgres

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// agentFixture is chatFixture with the agents' store and Olivia's agent Scout, a Member of Acme and a Developer on
// website.
type agentFixture struct {
	*chatFixture
	a     *AgentStore
	scout agents.Agent
}

func newAgentFixture(t *testing.T) *agentFixture {
	t.Helper()
	f := &agentFixture{chatFixture: newChatFixture(t)}
	f.a = NewAgentStore(f.pool)
	f.scout = f.created(t, "Scout", agents.Grant{RoleID: auth.RoleMember}, agents.Grant{RoleID: auth.RoleDeveloper, RepoID: f.website.ID})
	return f
}

// created records an agent of Olivia's called name with grants, and reads it back.
func (f *agentFixture) created(t *testing.T, name string, grants ...agents.Grant) agents.Agent {
	t.Helper()
	ctx := context.Background()
	a := agents.Agent{ID: v7(), OrgID: f.acme.ID, Name: name, Description: "Finds things", Instructions: "Be brief.",
		CreatedBy: &agents.Principal{ID: f.olivia}, Grants: grants}
	if err := f.a.CreateAgent(ctx, f.olivia, a); err != nil {
		t.Fatal(err)
	}
	got, err := f.a.Agent(ctx, f.acme.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// assigned assigns Scout a task of Max's titled title, perhaps on feature and a thread of its chat.
func (f *agentFixture) assigned(t *testing.T, title string, onFeature bool, thread uuid.UUID) agents.Task {
	t.Helper()
	task := agents.Task{ID: v7(), OrgID: f.acme.ID, Agent: agents.Principal{ID: f.scout.ID}, Kind: agents.KindAssigned,
		AssignedBy: &agents.Principal{ID: f.max}, Title: title, Instructions: "Look."}
	if onFeature {
		task.RepoID, task.BranchID, task.ThreadID = f.website.ID, f.feature.BranchID, thread
	}
	got, err := f.a.Assign(context.Background(), f.max, task)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAgentStoreCreate(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	s := f.scout
	if s.Name != "Scout" || s.Description != "Finds things" || s.Instructions != "Be brief." || s.BuiltIn || s.Version != 1 ||
		s.CreatedBy == nil || s.CreatedBy.ID != f.olivia || s.CreatedBy.DisplayName == "" {
		t.Errorf("Agent() = %+v", s)
	}
	want := []agents.Grant{{RoleID: auth.RoleMember, RoleName: "Member"}, {RoleID: auth.RoleDeveloper, RoleName: "Developer", RepoID: f.website.ID, RepoSlug: "website"}}
	if len(s.Grants) != 2 || s.Grants[0] != want[0] || s.Grants[1] != want[1] {
		t.Errorf("Agent().Grants = %+v, want %+v", s.Grants, want)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM principals WHERE id = $1 AND kind = 'agent' AND org_id = $2 AND display_name = 'Scout'", s.ID, f.acme.ID); n != 1 {
		t.Errorf("Scout's principals = %d, want 1 of kind agent in Acme", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'agents' AND principal_id = $1", f.olivia); n != 1 {
		t.Errorf("Olivia's agents audit rows = %d, want 1", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = 'agent.created' AND subject_id = $1", s.ID); n != 1 {
		t.Errorf("agent.created events = %d, want 1", n)
	}
	dup := agents.Agent{ID: v7(), OrgID: f.acme.ID, Name: "SCOUT"}
	if err := f.a.CreateAgent(ctx, f.olivia, dup); !errors.Is(err, agents.ErrNameTaken) {
		t.Errorf("CreateAgent(SCOUT) = %v, want %v", err, agents.ErrNameTaken)
	}
	f.created(t, "Archivist")
	list, err := f.a.Agents(ctx, f.acme.ID)
	if err != nil || len(list) != 2 || list[0].Name != "Archivist" || list[1].Name != "Scout" || len(list[1].Grants) != 2 || len(list[0].Grants) != 0 {
		t.Errorf("Agents() = %+v, %v; want Archivist, then Scout with its grants", list, err)
	}
	if _, err := f.a.Agent(ctx, uuid.New(), s.ID); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Agent(of another organization) = %v, want %v", err, orgs.ErrNotFound)
	}
}

func TestAgentStoreUpdate(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	f.created(t, "Archivist")
	name, empty := "Ranger", ""
	changed, err := f.a.UpdateAgent(ctx, f.max, f.acme.ID, f.scout.ID, 1, agents.Change{Name: &name, Instructions: &empty})
	if err != nil || changed.Name != "Ranger" || changed.Description != "Finds things" || changed.Instructions != "" || changed.Version != 2 ||
		len(changed.Grants) != 2 {
		t.Fatalf("UpdateAgent() = %+v, %v", changed, err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM principals WHERE id = $1 AND display_name = 'Ranger'", f.scout.ID); n != 1 {
		t.Error("the agent's principal kept its old name")
	}
	if _, err := f.a.UpdateAgent(ctx, f.max, f.acme.ID, f.scout.ID, 1, agents.Change{Name: &name}); !errors.Is(err, agents.ErrChanged) {
		t.Errorf("UpdateAgent(from version 1) = %v, want %v", err, agents.ErrChanged)
	}
	taken := "archivist"
	if _, err := f.a.UpdateAgent(ctx, f.max, f.acme.ID, f.scout.ID, agents.AnyVersion, agents.Change{Name: &taken}); !errors.Is(err, agents.ErrNameTaken) {
		t.Errorf("UpdateAgent(to a name taken) = %v, want %v", err, agents.ErrNameTaken)
	}
	if _, err := f.a.UpdateAgent(ctx, f.max, uuid.New(), f.scout.ID, agents.AnyVersion, agents.Change{Name: &name}); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("UpdateAgent(of another organization) = %v, want %v", err, orgs.ErrNotFound)
	}
}

func TestAgentStoreWhere(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	if slug, err := f.a.Repository(ctx, f.acme.ID, f.website.ID); err != nil || slug != "website" {
		t.Errorf("Repository() = %q, %v", slug, err)
	}
	if _, err := f.a.Repository(ctx, uuid.New(), f.website.ID); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Repository(of another organization) = %v, want %v", err, orgs.ErrNotFound)
	}
	if id, name, err := f.a.Branch(ctx, f.website.ID, uuid.Nil, "feature"); err != nil || id != f.feature.BranchID || name != "feature" {
		t.Errorf("Branch(feature) = %s, %q, %v", id, name, err)
	}
	if id, name, err := f.a.Branch(ctx, f.website.ID, f.feature.BranchID, ""); err != nil || id != f.feature.BranchID || name != "feature" {
		t.Errorf("Branch(by ID) = %s, %q, %v", id, name, err)
	}
	for _, name := range []string{"old", "nothing"} {
		if _, _, err := f.a.Branch(ctx, f.website.ID, uuid.Nil, name); !errors.Is(err, orgs.ErrNotFound) {
			t.Errorf("Branch(%s) = %v, want %v", name, err, orgs.ErrNotFound)
		}
	}
	thread := f.started(t, f.max, "Why?")
	if id, err := f.a.ThreadBranch(ctx, thread.ID); err != nil || id != f.feature.BranchID {
		t.Errorf("ThreadBranch() = %s, %v", id, err)
	}
	if roles, err := f.a.Roles(ctx, f.acme.ID, auth.ScopeRepository); err != nil || len(roles) == 0 || roles[0].ID != auth.RoleReader {
		t.Errorf("Roles(repository) = %+v, %v; want the repositories' built-in roles first", roles, err)
	}
	if roles, err := f.a.Roles(ctx, f.acme.ID, auth.ScopeOrganization); err != nil || len(roles) != 3 || roles[0].ID != auth.RoleGuest {
		t.Errorf("Roles(organization) = %+v, %v; want the organizations' built-in roles first", roles, err)
	}
}

func TestAgentStoreAssign(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	thread := f.started(t, f.max, "Why?")
	first, second := f.assigned(t, "Look around", false, uuid.Nil), f.assigned(t, "Answer the thread", true, thread.ID)
	if first.Number != 1 || first.State != agents.TaskQueued || first.Agent.DisplayName != "Scout" || first.AssignedBy == nil ||
		first.AssignedBy.ID != f.max || first.RepoID != uuid.Nil || first.Kind != agents.KindAssigned || first.Version != 1 || first.ByCobranch() {
		t.Errorf("Assign() = %+v", first)
	}
	if second.Number != 2 || second.RepoSlug != "website" || second.Branch != "feature" || second.ThreadID != thread.ID {
		t.Errorf("Assign(on feature) = %+v", second)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'agent_tasks' AND principal_id = $1", f.max); n != 2 {
		t.Errorf("Max's task audit rows = %d, want 2", n)
	}
	all := []uuid.UUID{f.website.ID, f.secret.ID}
	tests := []struct {
		name     string
		filter   agents.TaskFilter
		readable []uuid.UUID
		hidden   []string
		want     []int64
	}{
		{name: "every task, newest first", readable: all, want: []int64{2, 1}},
		{name: "none on a repository the viewer doesn't read", readable: nil, want: []int64{1}},
		{name: "none of a hidden kind", readable: all, hidden: []string{agents.KindAssigned}},
		{name: "those of kinds not hidden", readable: all, hidden: []string{"issue"}, want: []int64{2, 1}},
		{name: "on website", filter: agents.TaskFilter{RepoID: f.website.ID}, readable: all, want: []int64{2}},
		{name: "Scout's", filter: agents.TaskFilter{AgentID: f.scout.ID}, readable: all, want: []int64{2, 1}},
		{name: "another agent's", filter: agents.TaskFilter{AgentID: uuid.New()}, readable: all},
		{name: "queued", filter: agents.TaskFilter{States: []agents.TaskState{agents.TaskQueued}}, readable: all, want: []int64{2, 1}},
		{name: "ended", filter: agents.TaskFilter{States: []agents.TaskState{agents.TaskSucceeded, agents.TaskCancelled}}, readable: all},
		{name: "before the second", filter: agents.TaskFilter{Before: 2}, readable: all, want: []int64{1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list, err := f.a.Tasks(ctx, f.acme.ID, tt.filter, tt.readable, tt.hidden, 10)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]int64, 0, len(list))
			for _, task := range list {
				got = append(got, task.Number)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Tasks() = %v, want %v", got, tt.want)
			}
		})
	}
	if got, err := f.a.Task(ctx, f.acme.ID, 2); err != nil || got.ID != second.ID || got.Branch != "feature" {
		t.Errorf("Task(2) = %+v, %v", got, err)
	}
	if _, err := f.a.Task(ctx, f.acme.ID, 3); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Task(3) = %v, want %v", err, orgs.ErrNotFound)
	}
}

func TestAgentStoreAssignNumbers(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	var wg sync.WaitGroup
	numbers := make([]int64, 8)
	for i := range numbers {
		wg.Go(func() { numbers[i] = f.assigned(t, "At once", false, uuid.Nil).Number })
	}
	wg.Wait()
	seen := map[int64]bool{}
	for _, n := range numbers {
		if n < 1 || n > int64(len(numbers)) || seen[n] {
			t.Fatalf("numbers given at once = %v, want 1 to %d, each once", numbers, len(numbers))
		}
		seen[n] = true
	}
}

func TestAgentStoreAssignInATransaction(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	rollBack := errors.New("roll back")
	err := transact(ctx, f.pool, queries.New(f.pool), "another slice's change", f.max, func(ctx context.Context, _ *queries.Queries) error {
		task := agents.Task{ID: v7(), OrgID: f.acme.ID, Agent: agents.Principal{ID: f.scout.ID}, Kind: agents.KindAssigned,
			AssignedBy: &agents.Principal{ID: f.max}, Title: "Within"}
		if _, err := f.a.Assign(ctx, f.max, task); err != nil {
			return err
		}
		return rollBack
	})
	if !errors.Is(err, rollBack) {
		t.Fatalf("the transaction = %v, want %v", err, rollBack)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM agent_tasks WHERE org_id = $1", f.acme.ID); n != 0 {
		t.Errorf("tasks after the transaction rolled back = %d, want 0", n)
	}
}

func TestAgentStoreCancel(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	f.assigned(t, "Look around", false, uuid.Nil)
	if _, err := f.a.Cancel(ctx, f.olivia, f.acme.ID, 1, 2); !errors.Is(err, agents.ErrChanged) {
		t.Errorf("Cancel(from version 2) = %v, want %v", err, agents.ErrChanged)
	}
	cancelled, err := f.a.Cancel(ctx, f.olivia, f.acme.ID, 1, 1)
	if err != nil || cancelled.State != agents.TaskCancelled || cancelled.CancelledBy == nil || cancelled.CancelledBy.ID != f.olivia ||
		cancelled.EndedAt.IsZero() || cancelled.Version != 2 {
		t.Fatalf("Cancel() = %+v, %v", cancelled, err)
	}
	if _, err := f.a.Cancel(ctx, f.olivia, f.acme.ID, 1, agents.AnyVersion); !errors.Is(err, agents.ErrEnded) {
		t.Errorf("Cancel() again = %v, want %v", err, agents.ErrEnded)
	}
	if _, err := f.a.Cancel(ctx, f.olivia, f.acme.ID, 9, agents.AnyVersion); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Cancel(9) = %v, want %v", err, orgs.ErrNotFound)
	}
}

func TestAgentStoreDelete(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	f.assigned(t, "Look around", false, uuid.Nil)
	if err := f.a.DeleteAgent(ctx, f.olivia, f.acme.ID, f.scout.ID, 2); !errors.Is(err, agents.ErrChanged) {
		t.Errorf("DeleteAgent(from version 2) = %v, want %v", err, agents.ErrChanged)
	}
	if err := f.a.DeleteAgent(ctx, f.olivia, f.acme.ID, f.scout.ID, 1); err != nil {
		t.Fatalf("DeleteAgent() = %v", err)
	}
	for table, query := range map[string]string{
		"principals": "SELECT count(*) FROM principals WHERE id = $1", "agents": "SELECT count(*) FROM agents WHERE principal_id = $1",
		"grants": "SELECT count(*) FROM grants WHERE principal_id = $1", "agent_tasks": "SELECT count(*) FROM agent_tasks WHERE agent_id = $1",
	} {
		if n := count(t, f.pool, query, f.scout.ID); n != 0 {
			t.Errorf("%s left of the agent = %d, want 0", table, n)
		}
	}
	if err := f.a.DeleteAgent(ctx, f.olivia, f.acme.ID, f.scout.ID, agents.AnyVersion); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("DeleteAgent() again = %v, want %v", err, orgs.ErrNotFound)
	}
}
