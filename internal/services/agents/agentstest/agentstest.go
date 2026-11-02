// Package agentstest holds a fake of the agents domain's port, for the tests of its service and of the adapters that
// serve it: a Store in memory with the built-in roles.
package agentstest

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
)

// Repository is one of an organization's repositories.
type Repository struct {
	Org  uuid.UUID
	Slug string
}

// Branch is a branch of a repository; Deleted once a push deleted it.
type Branch struct {
	Repo    uuid.UUID
	Name    string
	Deleted bool
}

// Store is an agents.Store in memory. OrgRoles and RepoRoles are the roles the organizations grant at each scope;
// Repos, Branches and Threads (each thread's branch) are where tasks work; Now is the time it records changes at, and
// Events the events it recorded. It is safe for concurrent use.
type Store struct {
	mu        sync.Mutex
	agents    map[uuid.UUID]agents.Agent
	tasks     []agents.Task
	numbers   map[uuid.UUID]int64
	OrgRoles  []orgs.Role
	RepoRoles []orgs.Role
	Repos     map[uuid.UUID]Repository
	Branches  map[uuid.UUID]Branch
	Threads   map[uuid.UUID]uuid.UUID
	Names     map[uuid.UUID]string
	providers map[uuid.UUID]map[string]agents.ModelProvider
	secrets   map[uuid.UUID]Secret
	entries   map[uuid.UUID][]agents.Entry
	// OrgSlugs are the organizations' slugs, by ID.
	OrgSlugs map[uuid.UUID]string
	Events   []string
	Now      time.Time
}

var _ agents.Store = (*Store)(nil)

// NewStore returns an empty Store with orgstest's organization and repository roles.
func NewStore() *Store {
	return &Store{agents: map[uuid.UUID]agents.Agent{}, numbers: map[uuid.UUID]int64{},
		OrgRoles:  []orgs.Role{orgstest.Owner, orgstest.Member, orgstest.Guest, orgstest.Manager},
		RepoRoles: []orgs.Role{orgstest.Reader, orgstest.Developer, orgstest.Maintainer, orgstest.Automation},
		Repos:     map[uuid.UUID]Repository{}, Branches: map[uuid.UUID]Branch{}, Threads: map[uuid.UUID]uuid.UUID{},
		Names: map[uuid.UUID]string{}, providers: map[uuid.UUID]map[string]agents.ModelProvider{}, secrets: map[uuid.UUID]Secret{},
		entries: map[uuid.UUID][]agents.Entry{}, OrgSlugs: map[uuid.UUID]string{}}
}

// AddAgent records a, as CreateAgent would without checking its name, and answers it as the store keeps it.
func (s *Store) AddAgent(a agents.Agent) agents.Agent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	a.CreatedAt, a.UpdatedAt, a.Version = s.Now, s.Now, max(a.Version, 1)
	s.agents[a.ID] = a
	return a
}

// KeptTasks answers every task it keeps, of every organization, oldest first.
func (s *Store) KeptTasks() []agents.Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.tasks)
}

// SetState moves the organization's task of number to state, as the controller would.
func (s *Store) SetState(orgID uuid.UUID, number int64, state agents.TaskState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.task(orgID, number); i >= 0 {
		s.tasks[i].State = state
		s.tasks[i].Version++
		if !state.Active() {
			s.tasks[i].EndedAt = s.Now
		}
	}
}

// Roles lists OrgRoles or RepoRoles.
func (s *Store) Roles(_ context.Context, _ uuid.UUID, scope auth.Scope) ([]orgs.Role, error) {
	if scope == auth.ScopeRepository {
		return slices.Clone(s.RepoRoles), nil
	}
	return slices.Clone(s.OrgRoles), nil
}

// Repository answers the slug of one of the organization's repositories.
func (s *Store) Repository(_ context.Context, orgID, repoID uuid.UUID) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.Repos[repoID]
	if !ok || r.Org != orgID {
		return "", orgs.ErrNotFound
	}
	return r.Slug, nil
}

// Agents lists the organization's agents by name.
func (s *Store) Agents(_ context.Context, orgID uuid.UUID) ([]agents.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []agents.Agent
	for _, a := range s.agents {
		if a.OrgID == orgID {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b agents.Agent) int {
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.ID.String(), b.ID.String()))
	})
	return out, nil
}

// Agent finds one of the organization's agents.
func (s *Store) Agent(_ context.Context, orgID, id uuid.UUID) (agents.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[id]
	if !ok || a.OrgID != orgID {
		return agents.Agent{}, orgs.ErrNotFound
	}
	return a, nil
}

// taken reports whether another of the organization's agents than id has the name, in any case.
func (s *Store) taken(orgID, id uuid.UUID, name string) bool {
	for _, a := range s.agents {
		if a.OrgID == orgID && a.ID != id && strings.EqualFold(a.Name, name) {
			return true
		}
	}
	return false
}

// CreateAgent records a; ErrNameTaken for a name taken.
func (s *Store) CreateAgent(_ context.Context, actor uuid.UUID, a agents.Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.taken(a.OrgID, a.ID, a.Name) {
		return agents.ErrNameTaken
	}
	a.CreatedAt, a.UpdatedAt, a.Version = s.Now, s.Now, 1
	slices.SortStableFunc(a.Grants, func(x, y agents.Grant) int { return cmp.Compare(x.RepoSlug, y.RepoSlug) })
	s.agents[a.ID] = a
	s.Names[a.ID] = a.Name
	s.Events = append(s.Events, "agent.created "+a.Name+" by "+s.Names[actor])
	return nil
}

// UpdateAgent changes one of the organization's agents at version.
func (s *Store) UpdateAgent(_ context.Context, actor, orgID, id uuid.UUID, version int64, c agents.Change) (agents.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[id]
	switch {
	case !ok || a.OrgID != orgID:
		return agents.Agent{}, orgs.ErrNotFound
	case version >= 0 && a.Version != version:
		return agents.Agent{}, agents.ErrChanged
	case c.Name != nil && s.taken(orgID, id, *c.Name):
		return agents.Agent{}, agents.ErrNameTaken
	}
	for field, value := range map[*string]*string{&a.Name: c.Name, &a.Description: c.Description, &a.Instructions: c.Instructions} {
		if value != nil {
			*field = *value
		}
	}
	a.UpdatedAt = s.Now
	a.Version++
	s.agents[id] = a
	s.Names[id] = a.Name
	s.Events = append(s.Events, "agent.updated "+a.Name+" by "+s.Names[actor])
	return a, nil
}

// DeleteAgent deletes one of the organization's agents at version, with its tasks.
func (s *Store) DeleteAgent(_ context.Context, actor, orgID, id uuid.UUID, version int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[id]
	switch {
	case !ok || a.OrgID != orgID:
		return orgs.ErrNotFound
	case version >= 0 && a.Version != version:
		return agents.ErrChanged
	}
	delete(s.agents, id)
	s.tasks = slices.DeleteFunc(s.tasks, func(t agents.Task) bool { return t.Agent.ID == id })
	s.Events = append(s.Events, "agent.deleted "+a.Name+" by "+s.Names[actor])
	return nil
}

// Branch finds one of the repository's live branches by ID, or else by name.
func (s *Store) Branch(_ context.Context, repoID, id uuid.UUID, name string) (uuid.UUID, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for bid, b := range s.Branches {
		if b.Repo == repoID && !b.Deleted && (bid == id || (id == uuid.Nil && b.Name == name)) {
			return bid, b.Name, nil
		}
	}
	return uuid.Nil, "", orgs.ErrNotFound
}

// ThreadBranch answers the branch of a thread in Threads.
func (s *Store) ThreadBranch(_ context.Context, threadID uuid.UUID) (uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.Threads[threadID]
	if !ok {
		return uuid.Nil, orgs.ErrNotFound
	}
	return b, nil
}

// Tasks lists at most limit of the organization's tasks, newest first, that match f, name no repository or one of
// readable, and are of none of the kinds in hidden.
func (s *Store) Tasks(_ context.Context, orgID uuid.UUID, f agents.TaskFilter, readable []uuid.UUID, hidden []string, limit int) ([]agents.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []agents.Task
	for _, t := range slices.Backward(s.tasks) {
		match := t.OrgID == orgID && (f.AgentID == uuid.Nil || t.Agent.ID == f.AgentID) &&
			(len(f.States) == 0 || slices.Contains(f.States, t.State)) && (f.RepoID == uuid.Nil || t.RepoID == f.RepoID) &&
			(t.RepoID == uuid.Nil || slices.Contains(readable, t.RepoID)) && !slices.Contains(hidden, t.Kind) &&
			(f.Before == 0 || t.Number < f.Before)
		if match && len(out) < limit {
			out = append(out, t)
		}
	}
	return out, nil
}

// task finds the index of the organization's task of number; -1 for none.
func (s *Store) task(orgID uuid.UUID, number int64) int {
	return slices.IndexFunc(s.tasks, func(t agents.Task) bool { return t.OrgID == orgID && t.Number == number })
}

// Task finds one of the organization's tasks by its number.
func (s *Store) Task(_ context.Context, orgID uuid.UUID, number int64) (agents.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.task(orgID, number)
	if i < 0 {
		return agents.Task{}, orgs.ErrNotFound
	}
	return s.tasks[i], nil
}

// TaskByID finds a task by its ID, with its organization's slug from OrgSlugs and its agent's instructions and email.
func (s *Store) TaskByID(_ context.Context, id uuid.UUID) (agents.Work, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.tasks, func(t agents.Task) bool { return t.ID == id })
	if i < 0 {
		return agents.Work{}, orgs.ErrNotFound
	}
	t := s.tasks[i]
	return agents.Work{Task: t, OrgSlug: s.OrgSlugs[t.OrgID], AgentInstructions: s.agents[t.Agent.ID].Instructions,
		AgentEmail: agents.Email(t.Agent.ID)}, nil
}

// Entries lists at most limit of a task's entries numbered above after.
func (s *Store) Entries(_ context.Context, taskID uuid.UUID, after, limit int) ([]agents.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.entries[taskID]
	out := []agents.Entry{}
	for _, e := range all[min(after, len(all)):] {
		if len(out) == limit {
			break
		}
		out = append(out, e)
	}
	return out, nil
}

// Append records e as the task's next entry, or answers the one numbered e.Seq, and moves the task as change says.
func (s *Store) Append(_ context.Context, actor, taskID uuid.UUID, e agents.Entry, change *agents.StateChange) (agents.Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.tasks, func(t agents.Task) bool { return t.ID == taskID })
	all := s.entries[taskID]
	switch {
	case i < 0:
		return agents.Entry{}, false, orgs.ErrNotFound
	case e.Seq <= len(all):
		return all[e.Seq-1], false, nil
	case !s.tasks[i].State.Active():
		return agents.Entry{}, false, agents.ErrEnded
	case e.Seq > len(all)+1:
		return agents.Entry{}, false, &orgs.ValidationError{Field: "seq", Code: "transcript.seq_invalid", Message: "not the next entry"}
	case change != nil && !slices.Contains(change.From(), s.tasks[i].State):
		return agents.Entry{}, false, agents.ErrTransition
	}
	e.CreatedAt = s.Now
	s.entries[taskID] = append(all, e)
	if change != nil {
		t := &s.tasks[i]
		t.State, t.Summary, t.Reason = change.State, change.Summary, change.Reason
		if !change.State.Active() {
			t.EndedAt = s.Now
		}
		t.Version++
		s.Events = append(s.Events, "task."+string(change.State)+" "+t.Title+" by "+s.Names[actor])
	}
	return e, true, nil
}

// Assign numbers t in its organization and records it.
func (s *Store) Assign(_ context.Context, actor uuid.UUID, t agents.Task) (agents.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.numbers[t.OrgID]++
	t.Number, t.CreatedAt, t.Version = s.numbers[t.OrgID], s.Now, 1
	s.tasks = append(s.tasks, t)
	s.Events = append(s.Events, "task.assigned "+t.Title+" by "+s.Names[actor])
	return t, nil
}

// Cancel cancels one of the organization's active tasks at version.
func (s *Store) Cancel(_ context.Context, actor, orgID uuid.UUID, number, version int64) (agents.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.task(orgID, number)
	switch {
	case i < 0:
		return agents.Task{}, orgs.ErrNotFound
	case !s.tasks[i].State.Active():
		return agents.Task{}, agents.ErrEnded
	case version >= 0 && s.tasks[i].Version != version:
		return agents.Task{}, agents.ErrChanged
	}
	t := &s.tasks[i]
	t.State, t.EndedAt, t.Reason = agents.TaskCancelled, s.Now, "Cancelled"
	t.CancelledBy = &agents.Principal{ID: actor, DisplayName: s.Names[actor]}
	t.Version++
	s.Events = append(s.Events, "task.cancelled "+t.Title+" by "+s.Names[actor])
	return *t, nil
}
