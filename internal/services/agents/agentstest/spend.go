package agentstest

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/orgs"
)

// modelKey names one of an organization's models.
type modelKey struct {
	org             uuid.UUID
	provider, model string
}

// AddModel records one of the organization's models, as PutModel would without checking its provider, and answers it
// as the store keeps it.
func (s *Store) AddModel(m agents.Model) agents.Model {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.Default {
		s.undefault(m.OrgID)
	}
	m.CreatedAt, m.UpdatedAt, m.Version = s.Now, s.Now, max(m.Version, 1)
	s.models[modelKey{m.OrgID, m.Provider, m.Name}] = m
	return s.withAgents(m)
}

// undefault makes none of the organization's models its default, under the lock.
func (s *Store) undefault(orgID uuid.UUID) {
	for k, m := range s.models {
		if k.org == orgID && m.Default {
			m.Default = false
			s.models[k] = m
		}
	}
}

// withAgents is m with the agents on it, by name, under the lock.
func (s *Store) withAgents(m agents.Model) agents.Model {
	m.Agents = []agents.Principal{}
	for _, a := range s.agents {
		if a.OrgID == m.OrgID && a.Provider == m.Provider && a.Model == m.Name {
			m.Agents = append(m.Agents, agents.Principal{ID: a.ID, DisplayName: a.Name})
		}
	}
	slices.SortFunc(m.Agents, func(a, b agents.Principal) int {
		return cmp.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName))
	})
	return m
}

// spending is t with the most it may spend, its agent's cap, under the lock.
func (s *Store) spending(t agents.Task) agents.Task {
	t.Cap = s.agents[t.Agent.ID].Cap
	return t
}

// Models lists the organization's models by provider and name, each with the agents on it.
func (s *Store) Models(_ context.Context, orgID uuid.UUID) ([]agents.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []agents.Model{}
	for k, m := range s.models {
		if k.org == orgID {
			out = append(out, s.withAgents(m))
		}
	}
	slices.SortFunc(out, func(a, b agents.Model) int {
		return cmp.Or(strings.Compare(a.Provider, b.Provider), strings.Compare(a.Name, b.Name))
	})
	return out, nil
}

// Model finds one of the organization's models, with the agents on it.
func (s *Store) Model(_ context.Context, orgID uuid.UUID, provider, name string) (agents.Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.models[modelKey{orgID, provider, name}]
	if !ok {
		return agents.Model{}, orgs.ErrNotFound
	}
	return s.withAgents(m), nil
}

// PutModel adds a model of a provider the organization added, or changes one, as the version says.
func (s *Store) PutModel(_ context.Context, actor, orgID uuid.UUID, provider, name string, in agents.ModelInput, version int64) (agents.Model, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.providers[orgID][provider]; !ok {
		return agents.Model{}, false, &orgs.ValidationError{Field: "provider", Code: "model.provider_invalid", Message: "the organization hasn't added this provider"}
	}
	k := modelKey{orgID, provider, name}
	m, ok := s.models[k]
	switch {
	case ok && (version == agents.NoVersion || (version > 0 && m.Version != version)), !ok && version != agents.NoVersion:
		return agents.Model{}, false, agents.ErrChanged
	case ok:
		m.Version++
	default:
		m = agents.Model{OrgID: orgID, Provider: provider, Name: name, CreatedBy: &agents.Principal{ID: actor, DisplayName: s.Names[actor]},
			CreatedAt: s.Now, Version: 1}
	}
	if in.Default {
		s.undefault(orgID)
	}
	m.InputPrice, m.OutputPrice, m.Default, m.UpdatedAt = in.InputPrice, in.OutputPrice, in.Default, s.Now
	s.models[k] = m
	s.Events = append(s.Events, "model.set "+provider+"/"+name+" by "+s.Names[actor])
	return s.withAgents(m), !ok, nil
}

// RemoveModel removes one of the organization's models unless agents run on it.
func (s *Store) RemoveModel(_ context.Context, actor, orgID uuid.UUID, provider, name string, version int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := modelKey{orgID, provider, name}
	m, ok := s.models[k]
	switch {
	case !ok:
		return orgs.ErrNotFound
	case version >= 0 && m.Version != version:
		return agents.ErrChanged
	case len(s.withAgents(m).Agents) > 0:
		return agents.ErrModelInUse
	}
	delete(s.models, k)
	s.Events = append(s.Events, "model.removed "+provider+"/"+name+" by "+s.Names[actor])
	return nil
}

// RecordSpend records a call of a task's model once per ID, priced as the store prices it, and ends the task Failed
// once it reaches its cap.
func (s *Store) RecordSpend(_ context.Context, taskID uuid.UUID, u agents.Usage) (agents.Spend, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.tasks, func(t agents.Task) bool { return t.ID == taskID })
	switch {
	case i < 0:
		return agents.Spend{}, orgs.ErrNotFound
	case !s.tasks[i].State.Active():
		return agents.Spend{}, agents.ErrEnded
	}
	t := s.spending(s.tasks[i])
	if !slices.ContainsFunc(s.calls[taskID], func(c call) bool { return c.id == u.ID }) {
		a := s.agents[t.Agent.ID]
		priced, ok := s.models[modelKey{t.OrgID, a.Provider, u.Model}]
		if !ok {
			priced = s.models[modelKey{t.OrgID, a.Provider, a.Model}]
		}
		cost := priced.Cost(u.Input, u.Output)
		s.calls[taskID] = append(s.calls[taskID], call{id: u.ID, provider: a.Provider, model: u.Model, input: u.Input, output: u.Output, cost: cost, at: s.Now})
		t.Spent += cost
		t.InputTokens, t.OutputTokens = t.InputTokens+u.Input, t.OutputTokens+u.Output
	}
	out := agents.Spend{Spent: t.Spent, Input: t.InputTokens, Output: t.OutputTokens, Cap: t.Cap}
	if out.Cap > 0 && out.Spent >= out.Cap {
		out.Ended = true
		t.State, t.Reason, t.EndedAt = agents.TaskFailed, agents.CapReason(out.Spent, out.Cap), s.Now
		t.Version++
		s.Events = append(s.Events, "task.failed "+t.Agent.DisplayName)
	}
	s.tasks[i] = t
	return out, nil
}

// call is a call of a task's model that the store recorded: its ID, its agent's provider and the model it asked, the
// tokens it read and wrote, what it cost and when.
type call struct {
	id              uuid.UUID
	provider, model string
	input, output   int64
	cost            agents.USD
	at              time.Time
}

// Spend reports what the organization's agents spent from since until until, as the database's queries do: each
// agent's tasks queued then and its calls then, the costliest of those tasks that names no repository or one of
// readable and is of none of the kinds in hidden, and each model's calls then.
func (s *Store) Spend(_ context.Context, orgID uuid.UUID, since, until time.Time, readable []uuid.UUID, hidden []string) (agents.Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in := func(at time.Time) bool { return !at.Before(since) && at.Before(until) }
	out := agents.Report{Since: since, Until: until, Agents: []agents.AgentSpend{}, Models: s.modelSpend(orgID, in)}
	for _, a := range s.agents {
		if a.OrgID == orgID {
			out.Agents = append(out.Agents, s.agentSpend(a, in, func(t agents.Task) bool {
				return (t.RepoID == uuid.Nil || slices.Contains(readable, t.RepoID)) && !slices.Contains(hidden, t.Kind)
			}))
		}
	}
	slices.SortFunc(out.Agents, func(a, b agents.AgentSpend) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Agent.DisplayName), strings.ToLower(b.Agent.DisplayName)), strings.Compare(a.Agent.ID.String(), b.Agent.ID.String()))
	})
	return out, nil
}

// agentSpend is what agent a spent in a report's span, whose moments in says, its costliest task one that shown keeps,
// under the lock.
func (s *Store) agentSpend(a agents.Agent, in func(time.Time) bool, shown func(agents.Task) bool) agents.AgentSpend {
	out := agents.AgentSpend{Agent: agents.Principal{ID: a.ID, DisplayName: a.Name}, Provider: a.Provider, Model: a.Model, Cap: a.Cap}
	for _, t := range s.tasks {
		if t.Agent.ID != a.ID {
			continue
		}
		for _, c := range s.calls[t.ID] {
			if in(c.at) {
				out.Spent += c.cost
			}
		}
		if !in(t.CreatedAt) {
			continue
		}
		out.Tasks++
		switch {
		case t.State == agents.TaskFailed && a.Cap > 0 && t.Spent >= a.Cap:
			out.FailedOnCap++
		case t.State.Active():
			out.Active++
		}
		if shown(t) && (out.Costliest == nil || t.Spent > out.Costliest.Spent || (t.Spent == out.Costliest.Spent && t.Number > out.Costliest.Number)) {
			out.Costliest = &agents.TaskCost{Number: t.Number, Title: t.Title, Spent: t.Spent}
		}
	}
	return out
}

// modelSpend is what the calls of each of the organization's models, and of each other model its tasks asked, cost in
// a report's span, whose moments in says, by provider and name, under the lock.
func (s *Store) modelSpend(orgID uuid.UUID, in func(time.Time) bool) []agents.ModelSpend {
	by := map[modelKey]*agents.ModelSpend{}
	tasks := map[modelKey]map[uuid.UUID]bool{}
	for k := range s.models {
		if k.org == orgID {
			by[k] = &agents.ModelSpend{Provider: k.provider, Model: k.model}
		}
	}
	for _, t := range s.tasks {
		for _, c := range s.calls[t.ID] {
			if t.OrgID != orgID || !in(c.at) {
				continue
			}
			k := modelKey{orgID, c.provider, c.model}
			if by[k] == nil {
				by[k] = &agents.ModelSpend{Provider: c.provider, Model: c.model}
			}
			if tasks[k] == nil {
				tasks[k] = map[uuid.UUID]bool{}
			}
			tasks[k][t.ID] = true
			m := by[k]
			m.Tasks, m.Input, m.Output, m.Spent = int64(len(tasks[k])), m.Input+c.input, m.Output+c.output, m.Spent+c.cost
		}
	}
	out := []agents.ModelSpend{}
	for _, m := range by {
		out = append(out, *m)
	}
	slices.SortFunc(out, func(a, b agents.ModelSpend) int {
		return cmp.Or(strings.Compare(a.Provider, b.Provider), strings.Compare(a.Model, b.Model))
	})
	return out
}
