package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/orgs"
)

// modelProvider is the domain's view of a model_providers row, with its secret's and its creator's names.
func modelProvider(p queries.ModelProvider, secretName, creator *string) agents.ModelProvider {
	out := agents.ModelProvider{OrgID: p.OrgID, Name: p.Provider, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, Version: p.Version}
	if p.SecretID != nil && secretName != nil {
		out.SecretID, out.SecretName = *p.SecretID, *secretName
	}
	if p.CreatedBy != nil && creator != nil {
		out.CreatedBy = &agents.Principal{ID: *p.CreatedBy, DisplayName: *creator}
	}
	return out
}

// ModelProviders lists the providers the organization added, by name (agents.Store).
func (s *AgentStore) ModelProviders(ctx context.Context, orgID uuid.UUID) ([]agents.ModelProvider, error) {
	rows, err := s.q.ModelProviders(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing the model providers: %w", err)
	}
	out := make([]agents.ModelProvider, 0, len(rows))
	for _, r := range rows {
		out = append(out, modelProvider(r.ModelProvider, r.SecretName, r.CreatorName))
	}
	return out, nil
}

// ModelProvider finds a provider the organization added (agents.Store).
func (s *AgentStore) ModelProvider(ctx context.Context, orgID uuid.UUID, name string) (agents.ModelProvider, error) {
	return findModelProvider(ctx, s.q, orgID, name)
}

// findModelProvider finds a provider the organization added.
func findModelProvider(ctx context.Context, q *queries.Queries, orgID uuid.UUID, name string) (agents.ModelProvider, error) {
	r, err := q.ModelProvider(ctx, orgID, name)
	if err != nil {
		return agents.ModelProvider{}, fmt.Errorf("finding model provider %s: %w", name, notFound(err))
	}
	return modelProvider(r.ModelProvider, r.SecretName, r.CreatorName), nil
}

// SetModelProvider adds the provider for actor with its secret, or names another secret for the one there, under its
// row's lock, as the version says: agents.NoVersion only adds, agents.AnyVersion only changes, and any other version
// changes it at that version; agents.ErrChanged otherwise (agents.Store).
func (s *AgentStore) SetModelProvider(ctx context.Context, actor, orgID uuid.UUID, name string, secretID uuid.UUID, version int64) (agents.ModelProvider, bool, error) {
	var out agents.ModelProvider
	var created bool
	err := transact(ctx, s.pool, s.q, "setting the model provider", actor, func(ctx context.Context, q *queries.Queries) error {
		if version == agents.NoVersion {
			added, err := q.InsertModelProvider(ctx, queries.InsertModelProviderParams{OrgID: orgID, Provider: name, SecretID: &secretID, CreatedBy: &actor})
			switch {
			case err != nil:
				return fmt.Errorf("adding model provider %s: %w", name, err)
			case added == 0:
				return agents.ErrChanged
			}
			created = true
		} else {
			at, err := q.LockModelProvider(ctx, orgID, name)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				return agents.ErrChanged
			case err != nil:
				return fmt.Errorf("locking model provider %s: %w", name, err)
			case version > 0 && version != at:
				return agents.ErrChanged
			}
			if err := q.UpdateModelProvider(ctx, &secretID, orgID, name); err != nil {
				return fmt.Errorf("changing model provider %s: %w", name, err)
			}
		}
		kind := map[bool]string{true: "model_provider.added", false: "model_provider.changed"}[created]
		if err := orgEvent(ctx, q, &orgID, actor, kind, orgID, map[string]any{"provider": name, "secretId": secretID}); err != nil {
			return err
		}
		var err error
		out, err = findModelProvider(ctx, q, orgID, name)
		return err
	})
	return out, created, err
}

// RemoveModelProvider removes the provider for actor under its row's lock, at version or at any version for
// agents.AnyVersion; agents.ErrChanged, agents.ErrProviderInUse while agents run on it (agents.Store).
func (s *AgentStore) RemoveModelProvider(ctx context.Context, actor, orgID uuid.UUID, name string, version int64) error {
	return transact(ctx, s.pool, s.q, "removing the model provider", actor, func(ctx context.Context, q *queries.Queries) error {
		at, err := q.LockModelProvider(ctx, orgID, name)
		switch {
		case err != nil:
			return fmt.Errorf("locking model provider %s: %w", name, notFound(err))
		case version >= 0 && version != at:
			return agents.ErrChanged
		}
		using, err := q.ProviderAgents(ctx, orgID, &name)
		switch {
		case err != nil:
			return fmt.Errorf("counting the agents on model provider %s: %w", name, err)
		case using > 0:
			return agents.ErrProviderInUse
		}
		if err := q.DeleteModelProvider(ctx, orgID, name); err != nil {
			return fmt.Errorf("removing model provider %s: %w", name, err)
		}
		return orgEvent(ctx, q, &orgID, actor, "model_provider.removed", orgID, map[string]any{"provider": name})
	})
}

// SecretRepository finds one of the organization's secrets and answers the repository holding it: uuid.Nil for the
// organization's own (agents.Store).
func (s *AgentStore) SecretRepository(ctx context.Context, orgID, secretID uuid.UUID) (uuid.UUID, error) {
	repoID, err := s.q.SecretRepository(ctx, secretID, orgID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("finding secret %s: %w", secretID, notFound(err))
	}
	if repoID == nil {
		return uuid.Nil, nil
	}
	return *repoID, nil
}

// SetModel puts one of the organization's agents on a model of a provider it added, or on none with both empty, for
// actor, holding the provider against removal meanwhile, at version or at any version for agents.AnyVersion, and
// answers it; agents.ErrChanged, and a validation error for a provider the organization hasn't added (agents.Store).
func (s *AgentStore) SetModel(ctx context.Context, actor, orgID, id uuid.UUID, version int64, provider, model string) (agents.Agent, error) {
	return s.changeAgent(ctx, actor, orgID, id, "picking the agent's model", func(ctx context.Context, q *queries.Queries) error {
		params := queries.SetAgentModelParams{ID: id, OrgID: orgID, Version: version}
		if provider != "" {
			if _, err := q.ShareModelProvider(ctx, orgID, provider); errors.Is(err, pgx.ErrNoRows) {
				return &orgs.ValidationError{Field: "provider", Code: "agent.provider_invalid", Message: "the organization hasn't added this provider"}
			} else if err != nil {
				return fmt.Errorf("holding model provider %s: %w", provider, err)
			}
			params.Provider, params.Model = &provider, &model
		}
		if _, err := q.SetAgentModel(ctx, params); err != nil {
			return changed(err, fmt.Sprintf("picking agent %s's model", id))
		}
		return orgEvent(ctx, q, &orgID, actor, "agent.model_set", id, map[string]any{"provider": provider, "model": model})
	})
}

// SetSecrets names the secrets one of the organization's agents' tasks receive, for actor, at version or at any
// version for agents.AnyVersion, and answers it; agents.ErrChanged (agents.Store).
func (s *AgentStore) SetSecrets(ctx context.Context, actor, orgID, id uuid.UUID, version int64, named map[string]uuid.UUID) (agents.Agent, error) {
	if named == nil {
		named = map[string]uuid.UUID{}
	}
	encoded, err := json.Marshal(named)
	if err != nil {
		return agents.Agent{}, fmt.Errorf("encoding agent %s's secrets: %w", id, err)
	}
	return s.changeAgent(ctx, actor, orgID, id, "naming the agent's secrets", func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.SetAgentSecrets(ctx, queries.SetAgentSecretsParams{Secrets: encoded, ID: id, OrgID: orgID, Version: version}); err != nil {
			return changed(err, fmt.Sprintf("naming agent %s's secrets", id))
		}
		return orgEvent(ctx, q, &orgID, actor, "agent.secrets_set", id, map[string]any{"secrets": named})
	})
}

// changeAgent runs change on one of the organization's agents for actor under its lock, and answers the agent as it
// left it.
func (s *AgentStore) changeAgent(ctx context.Context, actor, orgID, id uuid.UUID, what string, change func(ctx context.Context, q *queries.Queries) error) (agents.Agent, error) {
	var out agents.Agent
	err := transact(ctx, s.pool, s.q, what, actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.LockAgent(ctx, id, orgID); err != nil {
			return fmt.Errorf("locking agent %s: %w", id, notFound(err))
		}
		if err := change(ctx, q); err != nil {
			return err
		}
		var err error
		out, err = findAgent(ctx, q, orgID, id)
		return err
	})
	return out, err
}

// changed is agents.ErrChanged for an update that found its row at another version, and err with what otherwise.
func changed(err error, what string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return agents.ErrChanged
	}
	return fmt.Errorf("%s: %w", what, err)
}
