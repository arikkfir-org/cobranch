package graphql

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/orgs"
)

// Agents is what the API needs of the agents' service.
type Agents interface {
	Agents(ctx context.Context, v agents.Viewer, orgID uuid.UUID) ([]agents.Agent, error)
	Agent(ctx context.Context, v agents.Viewer, orgID, id uuid.UUID) (agents.Agent, error)
	CreateAgent(ctx context.Context, v agents.Viewer, orgID uuid.UUID, in agents.Input) (agents.Agent, error)
	UpdateAgent(ctx context.Context, v agents.Viewer, orgID, id uuid.UUID, version int64, c agents.Change) (agents.Agent, error)
	DeleteAgent(ctx context.Context, v agents.Viewer, orgID, id uuid.UUID, version int64) error
	Tasks(ctx context.Context, v agents.Viewer, orgID uuid.UUID, f agents.TaskFilter, limit int) (agents.TaskPage, error)
	Task(ctx context.Context, v agents.Viewer, orgID uuid.UUID, number int64) (agents.Task, error)
	Assign(ctx context.Context, v agents.Viewer, orgID, agentID uuid.UUID, in agents.TaskInput) (agents.Task, error)
	CancelTask(ctx context.Context, v agents.Viewer, orgID uuid.UUID, number, version int64) (agents.Task, error)
}

func agentPrincipal(p *agents.Principal) *CommitPrincipal {
	if p == nil {
		return nil
	}
	return &CommitPrincipal{ID: p.ID.String(), DisplayName: p.DisplayName}
}

// text is a pointer to s, or nil for empty.
func text(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// orEmpty is *s, or empty for nil.
func orEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func agent(a agents.Agent) *Agent {
	out := &Agent{ID: a.ID.String(), Name: a.Name, Description: a.Description, Instructions: a.Instructions, BuiltIn: a.BuiltIn,
		CreatedBy: agentPrincipal(a.CreatedBy), CreatedAt: a.CreatedAt.UTC(), UpdatedAt: a.UpdatedAt.UTC(), Version: int(a.Version),
		Grants: make([]*AgentGrant, 0, len(a.Grants))}
	for _, g := range a.Grants {
		out.Grants = append(out.Grants, &AgentGrant{RoleID: g.RoleID.String(), RoleName: g.RoleName, RepositoryID: idOf(g.RepoID), RepositorySlug: text(g.RepoSlug)})
	}
	return out
}

func task(t agents.Task) *Task {
	out := &Task{ID: t.ID.String(), Number: int(t.Number), Agent: agentPrincipal(&t.Agent), Kind: t.Kind, AssignedBy: agentPrincipal(t.AssignedBy),
		Title: t.Title, Instructions: t.Instructions, RepositoryID: idOf(t.RepoID), RepositorySlug: text(t.RepoSlug), BranchID: idOf(t.BranchID),
		Branch: text(t.Branch), ThreadID: idOf(t.ThreadID), State: TaskState(strings.ToUpper(string(t.State))), Reason: t.Reason, Summary: t.Summary,
		CancelledBy: agentPrincipal(t.CancelledBy), CreatedAt: t.CreatedAt.UTC(), Version: int(t.Version)}
	if !t.StartedAt.IsZero() {
		at := t.StartedAt.UTC()
		out.StartedAt = &at
	}
	if !t.EndedAt.IsZero() {
		at := t.EndedAt.UTC()
		out.EndedAt = &at
	}
	return out
}

// inputID is the ID an input's field names, uuid.Nil for none, or a BAD_USER_INPUT error of code for one that names
// nothing.
func inputID(ctx context.Context, id *string, code string) (uuid.UUID, error) {
	if id == nil {
		return uuid.Nil, nil
	}
	parsed, err := uuid.Parse(*id)
	if err != nil {
		return uuid.Nil, refuse(ctx, badUserInput, code, "not an ID")
	}
	return parsed, nil
}

// agentVersion is the version expectedVersion names, agents.AnyVersion without it.
func agentVersion(ctx context.Context, expectedVersion *int) (int64, error) {
	if expectedVersion == nil {
		return agents.AnyVersion, nil
	}
	if *expectedVersion < 1 {
		return 0, fail(ctx, &orgs.ValidationError{Field: "expectedVersion", Code: "version.invalid", Message: "a version, from 1"}, "", "")
	}
	return int64(*expectedVersion), nil
}

// agentInput is in as the domain takes it.
func agentInput(ctx context.Context, in AgentInput) (agents.Input, error) {
	out := agents.Input{Name: in.Name, Description: orEmpty(in.Description), Instructions: orEmpty(in.Instructions)}
	for _, g := range in.Grants {
		role, err := inputID(ctx, &g.RoleID, "role.invalid")
		if err != nil {
			return out, err
		}
		repo, err := inputID(ctx, g.RepositoryID, "repository.invalid")
		if err != nil {
			return out, err
		}
		out.Grants = append(out.Grants, agents.Grant{RoleID: role, RepoID: repo})
	}
	return out, nil
}

// taskInput is in as the domain takes it, with the agent it names.
func taskInput(ctx context.Context, in TaskInput) (uuid.UUID, agents.TaskInput, error) {
	out := agents.TaskInput{Title: in.Title, Instructions: orEmpty(in.Instructions), Branch: orEmpty(in.Branch)}
	agentID, err := inputID(ctx, &in.AgentID, "agent.invalid")
	if err != nil {
		return uuid.Nil, out, err
	}
	for _, field := range []struct {
		id   *string
		into *uuid.UUID
		code string
	}{{in.RepositoryID, &out.RepoID, "repository.invalid"}, {in.BranchID, &out.BranchID, "branch.invalid"}, {in.ThreadID, &out.ThreadID, "thread.invalid"}} {
		if *field.into, err = inputID(ctx, field.id, field.code); err != nil {
			return uuid.Nil, out, err
		}
	}
	return agentID, out, nil
}

// taskFilter is the filter of a list of tasks.
func taskFilter(ctx context.Context, agent *string, states []TaskState, repositoryID *string, after *string) (agents.TaskFilter, error) {
	var f agents.TaskFilter
	var err error
	if f.AgentID, err = inputID(ctx, agent, "agent.invalid"); err != nil {
		return f, err
	}
	if f.RepoID, err = inputID(ctx, repositoryID, "repository.invalid"); err != nil {
		return f, err
	}
	for _, s := range states {
		f.States = append(f.States, agents.TaskState(strings.ToLower(string(s))))
	}
	f.Before, err = runsCursor(ctx, after)
	return f, err
}

// taskPage is page as the API shows it.
func taskPage(page agents.TaskPage) *TaskPage {
	out := &TaskPage{Tasks: mapAll(page.Tasks, task)}
	if page.Next != 0 {
		next := strconv.FormatInt(page.Next, 10)
		out.NextCursor = &next
	}
	return out
}
