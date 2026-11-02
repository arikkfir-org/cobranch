package graphql

import (
	"context"
	"fmt"
	"maps"
	"slices"
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
	ProviderNames() []string
	ModelProviders(ctx context.Context, v agents.Viewer, orgID uuid.UUID) ([]agents.ModelProvider, error)
	SetModelProvider(ctx context.Context, v agents.Viewer, orgID uuid.UUID, name string, secretID uuid.UUID, version int64) (agents.ModelProvider, bool, error)
	RemoveModelProvider(ctx context.Context, v agents.Viewer, orgID uuid.UUID, name string, version int64) error
	SetModel(ctx context.Context, v agents.Viewer, orgID, id uuid.UUID, provider, model string, version int64) (agents.Agent, error)
	SetSecrets(ctx context.Context, v agents.Viewer, orgID, id uuid.UUID, named map[string]uuid.UUID, version int64) (agents.Agent, error)
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
	if a.Provider != "" {
		out.Provider, out.Model = &a.Provider, &a.Model
	}
	out.Secrets = make([]*AgentSecret, 0, len(a.Secrets))
	for _, name := range slices.Sorted(maps.Keys(a.Secrets)) {
		out.Secrets = append(out.Secrets, &AgentSecret{Name: name, SecretID: a.Secrets[name].String()})
	}
	return out
}

func modelProvider(p agents.ModelProvider) *ModelProvider {
	return &ModelProvider{Name: p.Name, SecretID: idOf(p.SecretID), SecretName: text(p.SecretName), Broken: p.Broken(), CreatedBy: agentPrincipal(p.CreatedBy),
		CreatedAt: p.CreatedAt.UTC(), UpdatedAt: p.UpdatedAt.UTC(), Version: int(p.Version)}
}

// agentSecrets is the secrets an input names, by the environment variables they go in, or a BAD_USER_INPUT error for
// an ID that names nothing, or a variable named twice.
func agentSecrets(ctx context.Context, in []*AgentSecretInput) (map[string]uuid.UUID, error) {
	out := make(map[string]uuid.UUID, len(in))
	for _, s := range in {
		id, err := inputID(ctx, &s.SecretID, "agent.secrets_invalid")
		if err != nil {
			return nil, err
		}
		if _, twice := out[s.Name]; twice {
			return nil, refuse(ctx, badUserInput, "agent.secrets_invalid", fmt.Sprintf("%q is named twice", s.Name))
		}
		out[s.Name] = id
	}
	return out, nil
}

// agentTarget is the organization and the agent a mutation names, or a NOT_FOUND error for an ID that names nothing.
func agentTarget(ctx context.Context, organizationID, id string) (uuid.UUID, uuid.UUID, error) {
	orgID, err := parseID(ctx, organizationID, "organization")
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	agentID, err := parseID(ctx, id, "agent")
	return orgID, agentID, err
}

// providerVersion is the version expectedVersion names, where 0 adds the provider alone; agents.AnyVersion without it.
func providerVersion(ctx context.Context, expectedVersion *int) (int64, error) {
	if expectedVersion != nil && *expectedVersion == 0 {
		return agents.NoVersion, nil
	}
	return agentVersion(ctx, expectedVersion)
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
