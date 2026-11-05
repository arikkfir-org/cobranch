package agentstest

import (
	"cmp"
	"context"
	"slices"
	"strings"

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

// capped is a with its model's cap, under the lock.
func (s *Store) capped(a agents.Agent) agents.Agent {
	a.ModelCap = s.models[modelKey{a.OrgID, a.Provider, a.Model}].Cap
	return a
}

// spending is t with the most it may spend, under the lock.
func (s *Store) spending(t agents.Task) agents.Task {
	t.Cap = s.capped(s.agents[t.Agent.ID]).EffectiveCap()
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
	m.InputPrice, m.OutputPrice, m.Cap, m.Default, m.UpdatedAt = in.InputPrice, in.OutputPrice, in.Cap, in.Default, s.Now
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
	if !slices.Contains(s.calls[taskID], u.ID) {
		a := s.agents[t.Agent.ID]
		priced, ok := s.models[modelKey{t.OrgID, a.Provider, u.Model}]
		if !ok {
			priced = s.models[modelKey{t.OrgID, a.Provider, a.Model}]
		}
		s.calls[taskID] = append(s.calls[taskID], u.ID)
		t.Spent += priced.Cost(u.Input, u.Output)
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
