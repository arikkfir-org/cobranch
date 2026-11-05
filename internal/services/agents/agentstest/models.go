package agentstest

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/orgs"
)

// Secret is one of an organization's secrets, as agents name them: its repository's, or the organization's own with
// uuid.Nil.
type Secret struct {
	Org  uuid.UUID
	Repo uuid.UUID
	Name string
}

// AddSecret records one of the organization's secrets, of the repository repoID or the organization's own with
// uuid.Nil, and answers its ID.
func (s *Store) AddSecret(orgID, repoID uuid.UUID, name string) uuid.UUID {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := uuid.New()
	s.secrets[id] = Secret{Org: orgID, Repo: repoID, Name: name}
	return id
}

// DeleteSecret deletes a secret, leaving the providers it held the key of broken.
func (s *Store) DeleteSecret(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.secrets, id)
	for _, list := range s.providers {
		for name, p := range list {
			if p.SecretID == id {
				p.SecretID, p.SecretName = uuid.Nil, ""
				list[name] = p
			}
		}
	}
}

// ModelProviders lists the providers the organization added, by name.
func (s *Store) ModelProviders(_ context.Context, orgID uuid.UUID) ([]agents.ModelProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.SortedFunc(maps.Values(s.providers[orgID]), func(a, b agents.ModelProvider) int { return strings.Compare(a.Name, b.Name) }), nil
}

// ModelProvider finds a provider the organization added.
func (s *Store) ModelProvider(_ context.Context, orgID uuid.UUID, name string) (agents.ModelProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.providers[orgID][name]
	if !ok {
		return agents.ModelProvider{}, orgs.ErrNotFound
	}
	return p, nil
}

// SetModelProvider adds the provider, or names another secret for the one there, as the version says.
func (s *Store) SetModelProvider(_ context.Context, actor, orgID uuid.UUID, name string, secretID uuid.UUID, version int64) (agents.ModelProvider, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.providers[orgID][name]
	switch {
	case ok && (version == agents.NoVersion || (version > 0 && p.Version != version)), !ok && version != agents.NoVersion:
		return agents.ModelProvider{}, false, agents.ErrChanged
	case ok:
		p.Version++
	default:
		p = agents.ModelProvider{OrgID: orgID, Name: name, CreatedBy: &agents.Principal{ID: actor, DisplayName: s.Names[actor]}, CreatedAt: s.Now, Version: 1}
	}
	p.SecretID, p.SecretName, p.UpdatedAt = secretID, s.secrets[secretID].Name, s.Now
	if s.providers[orgID] == nil {
		s.providers[orgID] = map[string]agents.ModelProvider{}
	}
	s.providers[orgID][name] = p
	s.Events = append(s.Events, "model_provider.set "+name+" by "+s.Names[actor])
	return p, !ok, nil
}

// RemoveModelProvider removes the provider unless agents run on it.
func (s *Store) RemoveModelProvider(_ context.Context, actor, orgID uuid.UUID, name string, version int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.providers[orgID][name]
	switch {
	case !ok:
		return orgs.ErrNotFound
	case version >= 0 && p.Version != version:
		return agents.ErrChanged
	}
	for _, a := range s.agents {
		if a.OrgID == orgID && a.Provider == name {
			return agents.ErrProviderInUse
		}
	}
	delete(s.providers[orgID], name)
	s.Events = append(s.Events, "model_provider.removed "+name+" by "+s.Names[actor])
	return nil
}

// SecretRepository answers the repository holding one of the organization's secrets.
func (s *Store) SecretRepository(_ context.Context, orgID, secretID uuid.UUID) (uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	secret, ok := s.secrets[secretID]
	if !ok || secret.Org != orgID {
		return uuid.Nil, orgs.ErrNotFound
	}
	return secret.Repo, nil
}

// changeAgent applies change to one of the organization's agents at version and answers it, under the lock.
func (s *Store) changeAgent(actor, orgID, id uuid.UUID, version int64, event string, change func(*agents.Agent) error) (agents.Agent, error) {
	a, ok := s.agents[id]
	switch {
	case !ok || a.OrgID != orgID:
		return agents.Agent{}, orgs.ErrNotFound
	case version >= 0 && a.Version != version:
		return agents.Agent{}, agents.ErrChanged
	}
	if err := change(&a); err != nil {
		return agents.Agent{}, err
	}
	a.UpdatedAt = s.Now
	a.Version++
	s.agents[id] = a
	s.Events = append(s.Events, event+" "+a.Name+" by "+s.Names[actor])
	return a, nil
}

// SetModel puts one of the organization's agents on a model of a provider it added, or on none.
func (s *Store) SetModel(_ context.Context, actor, orgID, id uuid.UUID, version int64, provider, model string) (agents.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changeAgent(actor, orgID, id, version, "agent.model_set", func(a *agents.Agent) error {
		if _, ok := s.providers[orgID][provider]; provider != "" && !ok {
			return &orgs.ValidationError{Field: "provider", Code: "agent.provider_invalid", Message: "the organization hasn't added this provider"}
		}
		if _, ok := s.models[modelKey{orgID, provider, model}]; provider != "" && !ok {
			return &orgs.ValidationError{Field: "model", Code: "agent.model_invalid", Message: "the organization hasn't added this model"}
		}
		a.Provider, a.Model = provider, model
		return nil
	})
}

// SetSecrets names the secrets one of the organization's agents' tasks receive.
func (s *Store) SetSecrets(_ context.Context, actor, orgID, id uuid.UUID, version int64, named map[string]uuid.UUID) (agents.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changeAgent(actor, orgID, id, version, "agent.secrets_set", func(a *agents.Agent) error {
		a.Secrets = maps.Clone(named)
		return nil
	})
}
