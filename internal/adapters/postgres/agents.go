package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// AgentStore keeps an organization's agents and their tasks in PostgreSQL. A store method called with the context of
// another store's transaction joins it, so a slice that assigns tasks along with changes of its own records both at
// once.
type AgentStore struct {
	pool *pgxpool.Pool
	q    *queries.Queries
}

// NewAgentStore returns the agents' store on pool's database.
func NewAgentStore(pool *pgxpool.Pool) *AgentStore {
	return &AgentStore{pool: pool, q: queries.New(pool)}
}

var _ agents.Store = (*AgentStore)(nil)

// notFound is orgs.ErrNotFound for a row that isn't there, and err otherwise.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return orgs.ErrNotFound
	}
	return err
}

// nameTaken is agents.ErrNameTaken for a name another of the organization's agents has, and err otherwise.
func nameTaken(err error) error {
	var conflict *ConflictError
	if errors.As(err, &conflict) && conflict.Constraint == "agents_name" {
		return agents.ErrNameTaken
	}
	return err
}

// Roles lists the roles the organization grants at a scope, the organization or its repositories, the built-in ones
// first (agents.Store).
func (s *AgentStore) Roles(ctx context.Context, orgID uuid.UUID, scope auth.Scope) ([]orgs.Role, error) {
	list := s.q.OrganizationRoles
	if scope == auth.ScopeRepository {
		list = s.q.RepositoryRoles
	}
	rows, err := list(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing the roles: %w", err)
	}
	out := make([]orgs.Role, 0, len(rows))
	for _, r := range rows {
		out = append(out, role(r))
	}
	return out, nil
}

// Repository finds one of the organization's repositories by ID and answers its slug (agents.Store).
func (s *AgentStore) Repository(ctx context.Context, orgID, repoID uuid.UUID) (string, error) {
	slug, err := s.q.RepositorySlug(ctx, repoID, orgID)
	if err != nil {
		return "", fmt.Errorf("finding repository %s: %w", repoID, notFound(err))
	}
	return slug, nil
}

// agent is the domain's view of an agents row, with its creator's name.
func agent(a queries.Agent, creator *string) agents.Agent {
	out := agents.Agent{ID: a.PrincipalID, OrgID: a.OrgID, Name: a.Name, Description: a.Description, Instructions: a.Instructions,
		BuiltIn: a.BuiltIn, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, Version: a.Version}
	if a.CreatedBy != nil && creator != nil {
		out.CreatedBy = &agents.Principal{ID: *a.CreatedBy, DisplayName: *creator}
	}
	return out
}

// withGrants fills in the grants of each of list.
func withGrants(ctx context.Context, q *queries.Queries, list []agents.Agent) error {
	ids := make([]uuid.UUID, 0, len(list))
	at := make(map[uuid.UUID]int, len(list))
	for i, a := range list {
		ids, at[a.ID] = append(ids, a.ID), i
	}
	rows, err := q.AgentGrants(ctx, ids)
	if err != nil {
		return fmt.Errorf("listing the agents' grants: %w", err)
	}
	for _, r := range rows {
		g := agents.Grant{RoleID: r.RoleID, RoleName: r.RoleName}
		if r.RepoSlug != nil && r.ScopeID != nil {
			g.RepoID, g.RepoSlug = *r.ScopeID, *r.RepoSlug
		}
		a := &list[at[r.PrincipalID]]
		a.Grants = append(a.Grants, g)
	}
	return nil
}

// Agents lists the organization's agents by name, each with its grants (agents.Store).
func (s *AgentStore) Agents(ctx context.Context, orgID uuid.UUID) ([]agents.Agent, error) {
	rows, err := s.q.Agents(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing the agents: %w", err)
	}
	out := make([]agents.Agent, 0, len(rows))
	for _, r := range rows {
		out = append(out, agent(r.Agent, r.CreatorName))
	}
	return out, withGrants(ctx, s.q, out)
}

// Agent finds one of the organization's agents with its grants (agents.Store).
func (s *AgentStore) Agent(ctx context.Context, orgID, id uuid.UUID) (agents.Agent, error) {
	return findAgent(ctx, s.q, orgID, id)
}

// findAgent finds one of the organization's agents with its grants.
func findAgent(ctx context.Context, q *queries.Queries, orgID, id uuid.UUID) (agents.Agent, error) {
	row, err := q.Agent(ctx, id, orgID)
	if err != nil {
		return agents.Agent{}, fmt.Errorf("finding agent %s: %w", id, notFound(err))
	}
	out := []agents.Agent{agent(row.Agent, row.CreatorName)}
	if err := withGrants(ctx, q, out); err != nil {
		return agents.Agent{}, err
	}
	return out[0], nil
}

// CreateAgent records the agent, its principal and its grants for actor, under its organization's lock, as every
// grant is; agents.ErrNameTaken for a name taken (agents.Store).
func (s *AgentStore) CreateAgent(ctx context.Context, actor uuid.UUID, a agents.Agent) error {
	var creator *uuid.UUID
	if a.CreatedBy != nil {
		creator = &a.CreatedBy.ID
	}
	err := transact(ctx, s.pool, s.q, "recording the agent", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.LockOrganization(ctx, a.OrgID); err != nil {
			return fmt.Errorf("locking the organization: %w", notFound(err))
		}
		if err := q.InsertAgentPrincipal(ctx, queries.InsertAgentPrincipalParams{ID: a.ID, Name: a.Name, OrgID: &a.OrgID, CreatedBy: creator}); err != nil {
			return fmt.Errorf("inserting the agent's principal: %w", err)
		}
		if err := q.InsertAgent(ctx, queries.InsertAgentParams{ID: a.ID, OrgID: a.OrgID, Name: a.Name, Description: a.Description,
			Instructions: a.Instructions, BuiltIn: a.BuiltIn, CreatedBy: creator}); err != nil {
			return fmt.Errorf("inserting the agent: %w", err)
		}
		grants := make([]map[string]any, 0, len(a.Grants))
		for _, g := range a.Grants {
			scope, scopeID := auth.ScopeOrganization, a.OrgID
			if g.RepoID != uuid.Nil {
				scope, scopeID = auth.ScopeRepository, g.RepoID
			}
			if _, err := q.InsertGrant(ctx, queries.InsertGrantParams{PrincipalID: a.ID, RoleID: g.RoleID, Scope: string(scope), ScopeID: &scopeID,
				Source: string(auth.SourceGrant)}); err != nil {
				return fmt.Errorf("inserting the agent's grant: %w", err)
			}
			grants = append(grants, map[string]any{"role": g.RoleID, "scope": scope, "scopeId": scopeID})
		}
		return orgEvent(ctx, q, &a.OrgID, actor, "agent.created", a.ID, map[string]any{"name": a.Name, "grants": grants})
	})
	return nameTaken(err)
}

// UpdateAgent changes one of the organization's agents for actor, at version or at any version for
// agents.AnyVersion, and answers it; agents.ErrChanged, or agents.ErrNameTaken for a name taken (agents.Store).
func (s *AgentStore) UpdateAgent(ctx context.Context, actor, orgID, id uuid.UUID, version int64, c agents.Change) (agents.Agent, error) {
	var out agents.Agent
	err := transact(ctx, s.pool, s.q, "changing the agent", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.LockAgent(ctx, id, orgID); err != nil {
			return fmt.Errorf("locking agent %s: %w", id, notFound(err))
		}
		_, err := q.UpdateAgent(ctx, queries.UpdateAgentParams{Name: c.Name, Description: c.Description, Instructions: c.Instructions,
			ID: id, OrgID: orgID, Version: version})
		if errors.Is(err, pgx.ErrNoRows) {
			return agents.ErrChanged
		}
		if err != nil {
			return fmt.Errorf("changing agent %s: %w", id, err)
		}
		if c.Name != nil {
			if err := q.RenameAgentPrincipal(ctx, *c.Name, id); err != nil {
				return fmt.Errorf("renaming agent %s's principal: %w", id, err)
			}
		}
		if err := orgEvent(ctx, q, &orgID, actor, "agent.updated", id, map[string]any{"name": c.Name != nil, "description": c.Description != nil,
			"instructions": c.Instructions != nil}); err != nil {
			return err
		}
		out, err = findAgent(ctx, q, orgID, id)
		return err
	})
	return out, nameTaken(err)
}

// DeleteAgent deletes one of the organization's agents for actor, with its principal, grants and tasks, at version or
// at any version for agents.AnyVersion; agents.ErrChanged (agents.Store).
func (s *AgentStore) DeleteAgent(ctx context.Context, actor, orgID, id uuid.UUID, version int64) error {
	return transact(ctx, s.pool, s.q, "deleting the agent", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.LockOrganization(ctx, orgID); err != nil {
			return fmt.Errorf("locking the organization: %w", notFound(err))
		}
		at, err := q.LockAgent(ctx, id, orgID)
		if err != nil {
			return fmt.Errorf("locking agent %s: %w", id, notFound(err))
		}
		if version >= 0 && version != at {
			return agents.ErrChanged
		}
		if _, err := q.DeleteAgentPrincipal(ctx, id, orgID); err != nil {
			return fmt.Errorf("deleting agent %s: %w", id, err)
		}
		return orgEvent(ctx, q, &orgID, actor, "agent.deleted", id, nil)
	})
}

// Branch finds one of the repository's live branches, by ID or else by name, and answers both (agents.Store).
func (s *AgentStore) Branch(ctx context.Context, repoID, id uuid.UUID, name string) (uuid.UUID, string, error) {
	row, err := s.q.TaskBranch(ctx, repoID, idOrNil(id), name)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("finding the branch: %w", notFound(err))
	}
	return row.ID, row.Name, nil
}

// ThreadBranch answers the branch a chat thread belongs to, uuid.Nil for an issue's (agents.Store).
func (s *AgentStore) ThreadBranch(ctx context.Context, threadID uuid.UUID) (uuid.UUID, error) {
	id, err := s.q.ThreadBranch(ctx, threadID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("finding thread %s: %w", threadID, notFound(err))
	}
	if id == nil {
		return uuid.Nil, nil
	}
	return *id, nil
}

// taskRow is what a read of a task answers: the row and the names around it.
type taskRow struct {
	t                                   queries.AgentTask
	agent                               string
	assigner, canceller, repo, branchOf *string
}

// task is the domain's view of a task's row.
func task(r taskRow) agents.Task {
	t := r.t
	out := agents.Task{ID: t.ID, OrgID: t.OrgID, Number: t.Number, Agent: agents.Principal{ID: t.AgentID, DisplayName: r.agent}, Kind: t.Kind,
		Title: t.Title, Instructions: t.Instructions, State: agents.TaskState(t.State), Reason: t.Reason, Summary: t.Summary,
		CreatedAt: t.CreatedAt, StartedAt: zero(t.StartedAt), EndedAt: zero(t.EndedAt), Version: t.Version}
	out.AssignedBy = taskPrincipal(t.AssignedBy, r.assigner)
	out.CancelledBy = taskPrincipal(t.CancelledBy, r.canceller)
	if t.RepoID != nil && r.repo != nil {
		out.RepoID, out.RepoSlug = *t.RepoID, *r.repo
	}
	if t.BranchID != nil && r.branchOf != nil {
		out.BranchID, out.Branch = *t.BranchID, *r.branchOf
	}
	if t.ThreadID != nil {
		out.ThreadID = *t.ThreadID
	}
	if t.IssueID != nil {
		out.IssueID = *t.IssueID
	}
	return out
}

// taskPrincipal is the principal of id and name, or nil without either.
func taskPrincipal(id *uuid.UUID, name *string) *agents.Principal {
	if id == nil || name == nil {
		return nil
	}
	return &agents.Principal{ID: *id, DisplayName: *name}
}

// zero is *at, or the zero time for nil.
func zero(at *time.Time) time.Time {
	if at == nil {
		return time.Time{}
	}
	return *at
}

// Tasks lists a page of at most limit of the organization's tasks, newest first, that match the filter, either name
// no repository or one of readable, and are of none of the kinds in hidden (agents.Store).
func (s *AgentStore) Tasks(ctx context.Context, orgID uuid.UUID, f agents.TaskFilter, readable []uuid.UUID, hidden []string, limit int) ([]agents.Task, error) {
	p := queries.TasksParams{OrgID: orgID, States: make([]string, 0, len(f.States)), Readable: readable, Hidden: hidden, Lim: int32(min(limit, agents.MaxPage+1))} //nolint:gosec // a page is small
	if p.Readable == nil {
		p.Readable = []uuid.UUID{}
	}
	if p.Hidden == nil {
		p.Hidden = []string{}
	}
	if f.AgentID != uuid.Nil {
		p.AgentID = &f.AgentID
	}
	if f.RepoID != uuid.Nil {
		p.RepoID = &f.RepoID
	}
	for _, state := range f.States {
		p.States = append(p.States, string(state))
	}
	if f.Before > 0 {
		p.Before = &f.Before
	}
	rows, err := s.q.Tasks(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("listing the tasks: %w", err)
	}
	out := make([]agents.Task, 0, len(rows))
	for _, r := range rows {
		out = append(out, task(taskRow{r.AgentTask, r.AgentName, r.AssignerName, r.CancellerName, r.RepoSlug, r.BranchName}))
	}
	return out, nil
}

// Task finds one of the organization's tasks by its number (agents.Store).
func (s *AgentStore) Task(ctx context.Context, orgID uuid.UUID, number int64) (agents.Task, error) {
	return findTask(ctx, s.q, orgID, number)
}

// findTask finds one of the organization's tasks by its number.
func findTask(ctx context.Context, q *queries.Queries, orgID uuid.UUID, number int64) (agents.Task, error) {
	r, err := q.Task(ctx, orgID, number)
	if err != nil {
		return agents.Task{}, fmt.Errorf("finding task %d: %w", number, notFound(err))
	}
	return task(taskRow{r.AgentTask, r.AgentName, r.AssignerName, r.CancellerName, r.RepoSlug, r.BranchName}), nil
}

// Assign numbers the task in its organization and records it for actor, Queued, and answers it. Called with the
// context of another store's transaction, it records the task in that transaction (agents.Store).
func (s *AgentStore) Assign(ctx context.Context, actor uuid.UUID, t agents.Task) (agents.Task, error) {
	var out agents.Task
	err := transact(ctx, s.pool, s.q, "recording the task", actor, func(ctx context.Context, q *queries.Queries) error {
		number, err := q.NextTaskNumber(ctx, t.OrgID)
		if err != nil {
			return fmt.Errorf("numbering the task: %w", err)
		}
		p := queries.InsertTaskParams{ID: t.ID, OrgID: t.OrgID, Number: number, AgentID: t.Agent.ID, Kind: t.Kind, Title: t.Title,
			Instructions: t.Instructions, RepoID: idOrNil(t.RepoID), BranchID: idOrNil(t.BranchID), ThreadID: idOrNil(t.ThreadID),
			IssueID: idOrNil(t.IssueID)}
		if t.AssignedBy != nil {
			p.AssignedBy = &t.AssignedBy.ID
		}
		if err := q.InsertTask(ctx, p); err != nil {
			return fmt.Errorf("inserting the task: %w", err)
		}
		if err := orgEvent(ctx, q, &t.OrgID, actor, "task.assigned", t.ID, map[string]any{"number": number, "agent": t.Agent.ID, "kind": t.Kind}); err != nil {
			return err
		}
		out, err = findTask(ctx, q, t.OrgID, number)
		return err
	})
	return out, err
}

// idOrNil is &id, or nil for uuid.Nil.
func idOrNil(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// Cancel cancels one of the organization's active tasks for actor, at version or at any version for
// agents.AnyVersion, and answers it; agents.ErrEnded once it has ended, agents.ErrChanged (agents.Store).
func (s *AgentStore) Cancel(ctx context.Context, actor, orgID uuid.UUID, number, version int64) (agents.Task, error) {
	var out agents.Task
	err := transact(ctx, s.pool, s.q, "cancelling the task", actor, func(ctx context.Context, q *queries.Queries) error {
		row, err := q.CancelTask(ctx, queries.CancelTaskParams{CancelledBy: &actor, Reason: "Cancelled", OrgID: orgID, Number: number, Version: version})
		if errors.Is(err, pgx.ErrNoRows) {
			current, err := findTask(ctx, q, orgID, number)
			switch {
			case err != nil:
				return err
			case !current.State.Active():
				return agents.ErrEnded
			}
			return agents.ErrChanged
		}
		if err != nil {
			return fmt.Errorf("cancelling task %d: %w", number, err)
		}
		if err := orgEvent(ctx, q, &orgID, actor, "task.cancelled", row.ID, map[string]any{"number": number}); err != nil {
			return err
		}
		out, err = findTask(ctx, q, orgID, number)
		return err
	})
	return out, err
}
