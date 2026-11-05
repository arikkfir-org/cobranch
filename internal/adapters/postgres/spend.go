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

// usd is an amount the database keeps, or 0 for none.
func usd(v *int64) agents.USD {
	if v == nil {
		return 0
	}
	return agents.USD(*v)
}

// nullUSD is an amount as the database keeps it: null for 0, which is none.
func nullUSD(u agents.USD) *int64 {
	if u == 0 {
		return nil
	}
	v := int64(u)
	return &v
}

// model is the domain's view of a models row, with its creator's name and the agents on it.
func model(m queries.Model, creator *string, on []agents.Principal) agents.Model {
	out := agents.Model{OrgID: m.OrgID, Provider: m.Provider, Name: m.Model, InputPrice: agents.USD(m.InputPrice),
		OutputPrice: agents.USD(m.OutputPrice), Default: m.IsDefault, Agents: on, CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt, Version: m.Version}
	if out.Agents == nil {
		out.Agents = []agents.Principal{}
	}
	if m.CreatedBy != nil && creator != nil {
		out.CreatedBy = &agents.Principal{ID: *m.CreatedBy, DisplayName: *creator}
	}
	return out
}

// modelKey names one of an organization's models among the others.
type modelKey struct{ provider, model string }

// modelAgents answers the organization's agents that run on its models, by model and then name.
func modelAgents(ctx context.Context, q *queries.Queries, orgID uuid.UUID) (map[modelKey][]agents.Principal, error) {
	rows, err := q.ModelAgents(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing the agents on the models: %w", err)
	}
	out := map[modelKey][]agents.Principal{}
	for _, r := range rows {
		k := modelKey{r.Provider, r.Model}
		out[k] = append(out[k], agents.Principal{ID: r.PrincipalID, DisplayName: r.Name})
	}
	return out, nil
}

// Models lists the organization's models by provider and name, each with the agents on it (agents.Store).
func (s *AgentStore) Models(ctx context.Context, orgID uuid.UUID) ([]agents.Model, error) {
	rows, err := s.q.Models(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing the models: %w", err)
	}
	on, err := modelAgents(ctx, s.q, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]agents.Model, 0, len(rows))
	for _, r := range rows {
		out = append(out, model(r.Model, r.CreatorName, on[modelKey{r.Model.Provider, r.Model.Model}]))
	}
	return out, nil
}

// Model finds one of the organization's models, with the agents on it (agents.Store).
func (s *AgentStore) Model(ctx context.Context, orgID uuid.UUID, provider, name string) (agents.Model, error) {
	return findModel(ctx, s.q, orgID, provider, name)
}

// findModel finds one of the organization's models, with the agents on it.
func findModel(ctx context.Context, q *queries.Queries, orgID uuid.UUID, provider, name string) (agents.Model, error) {
	r, err := q.Model(ctx, orgID, provider, name)
	if err != nil {
		return agents.Model{}, fmt.Errorf("finding model %s of %s: %w", name, provider, notFound(err))
	}
	on, err := modelAgents(ctx, q, orgID)
	if err != nil {
		return agents.Model{}, err
	}
	return model(r.Model, r.CreatorName, on[modelKey{provider, name}]), nil
}

// PutModel adds a model of a provider the organization added to its models for actor, or changes one of them under
// its row's lock, as the version says: agents.NoVersion only adds, agents.AnyVersion only changes, and any other
// version changes it at that version; agents.ErrChanged otherwise, and a validation error for a provider the
// organization hasn't added. Making it the default makes the one before it no longer so (agents.Store).
func (s *AgentStore) PutModel(ctx context.Context, actor, orgID uuid.UUID, provider, name string, in agents.ModelInput, version int64) (agents.Model, bool, error) {
	var out agents.Model
	var created bool
	err := transact(ctx, s.pool, s.q, "setting the model", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.ShareModelProvider(ctx, orgID, provider); errors.Is(err, pgx.ErrNoRows) {
			return &orgs.ValidationError{Field: "provider", Code: "model.provider_invalid", Message: "the organization hasn't added this provider"}
		} else if err != nil {
			return fmt.Errorf("holding model provider %s: %w", provider, err)
		}
		if in.Default {
			// One default at a time, which the index on it holds even within the transaction.
			if err := q.UnsetDefaultModel(ctx, orgID, provider, name); err != nil {
				return fmt.Errorf("unsetting the default model: %w", err)
			}
		}
		var err error
		if created, err = putModel(ctx, q, actor, orgID, provider, name, in, version); err != nil {
			return err
		}
		kind := map[bool]string{true: "model.added", false: "model.changed"}[created]
		details := map[string]any{"provider": provider, "model": name, "inputPrice": in.InputPrice.String(),
			"outputPrice": in.OutputPrice.String(), "default": in.Default}
		if err := orgEvent(ctx, q, &orgID, actor, kind, orgID, details); err != nil {
			return err
		}
		out, err = findModel(ctx, q, orgID, provider, name)
		return err
	})
	return out, created, err
}

// putModel adds the model or changes it, as PutModel's version says, and answers whether it added it.
func putModel(ctx context.Context, q *queries.Queries, actor, orgID uuid.UUID, provider, name string, in agents.ModelInput, version int64) (bool, error) {
	if version == agents.NoVersion {
		added, err := q.InsertModel(ctx, queries.InsertModelParams{OrgID: orgID, Provider: provider, Model: name,
			InputPrice: int64(in.InputPrice), OutputPrice: int64(in.OutputPrice), IsDefault: in.Default, CreatedBy: &actor})
		switch {
		case err != nil:
			return false, fmt.Errorf("adding model %s of %s: %w", name, provider, err)
		case added == 0:
			return false, agents.ErrChanged
		}
		return true, nil
	}
	at, err := q.LockModel(ctx, orgID, provider, name)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, agents.ErrChanged
	case err != nil:
		return false, fmt.Errorf("locking model %s of %s: %w", name, provider, err)
	case version > 0 && version != at:
		return false, agents.ErrChanged
	}
	err = q.UpdateModel(ctx, queries.UpdateModelParams{InputPrice: int64(in.InputPrice), OutputPrice: int64(in.OutputPrice),
		IsDefault: in.Default, OrgID: orgID, Provider: provider, Model: name})
	if err != nil {
		return false, fmt.Errorf("changing model %s of %s: %w", name, provider, err)
	}
	return false, nil
}

// RemoveModel removes one of the organization's models for actor under its row's lock, at version or at any version
// for agents.AnyVersion; agents.ErrChanged, agents.ErrModelInUse while agents run on it (agents.Store).
func (s *AgentStore) RemoveModel(ctx context.Context, actor, orgID uuid.UUID, provider, name string, version int64) error {
	return transact(ctx, s.pool, s.q, "removing the model", actor, func(ctx context.Context, q *queries.Queries) error {
		at, err := q.LockModel(ctx, orgID, provider, name)
		switch {
		case err != nil:
			return fmt.Errorf("locking model %s of %s: %w", name, provider, notFound(err))
		case version >= 0 && version != at:
			return agents.ErrChanged
		}
		using, err := q.ModelAgentCount(ctx, orgID, &provider, &name)
		switch {
		case err != nil:
			return fmt.Errorf("counting the agents on model %s of %s: %w", name, provider, err)
		case using > 0:
			return agents.ErrModelInUse
		}
		if err := q.DeleteModel(ctx, orgID, provider, name); err != nil {
			return fmt.Errorf("removing model %s of %s: %w", name, provider, err)
		}
		return orgEvent(ctx, q, &orgID, actor, "model.removed", orgID, map[string]any{"provider": provider, "model": name})
	})
}

// RecordSpend records a call of a task's model under the task's lock, priced at its organization's prices for the
// reported model of its agent's provider, or for its agent's model when it has none for that one, and answers what
// the task spent. A task that reaches its agent's cap ends Failed, saying so, and a
// report that repeats an ID records nothing; agents.ErrEnded for a task that has ended (agents.Store).
func (s *AgentStore) RecordSpend(ctx context.Context, taskID uuid.UUID, u agents.Usage) (agents.Spend, error) {
	var out agents.Spend
	err := transact(ctx, s.pool, s.q, "recording the task's spend", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		t, err := q.LockSpendingTask(ctx, taskID)
		if err != nil {
			return fmt.Errorf("locking task %s: %w", taskID, notFound(err))
		}
		if !agents.TaskState(t.State).Active() {
			return agents.ErrEnded
		}
		provider := ""
		if t.Provider != nil {
			provider = *t.Provider
		}
		priced, err := spendPrices(ctx, q, t, provider, u.Model)
		if err != nil {
			return err
		}
		_, err = q.InsertSpend(ctx, queries.InsertSpendParams{TaskID: taskID, ID: u.ID, OrgID: t.OrgID, AgentID: t.AgentID,
			Provider: provider, Model: u.Model, InputTokens: u.Input, OutputTokens: u.Output, Cost: int64(priced.Cost(u.Input, u.Output))})
		if err != nil {
			return fmt.Errorf("recording call %s: %w", u.ID, err)
		}
		sum, err := q.TaskSpent(ctx, taskID)
		if err != nil {
			return fmt.Errorf("summing task %s's spend: %w", taskID, err)
		}
		out = agents.Spend{Spent: agents.USD(sum.Spent), Input: sum.InputTokens, Output: sum.OutputTokens, Cap: usd(t.Cap)}
		if out.Cap == 0 || out.Spent < out.Cap {
			return nil
		}
		out.Ended = true
		_, err = failTask(ctx, q, taskID, agents.CapReason(out.Spent, out.Cap))
		return err
	})
	return out, err
}

// spendPrices answers the prices of the organization's model that the task's call asked, of its agent's provider, or
// its agent's model's when the organization has none for that one; none when it has neither.
func spendPrices(ctx context.Context, q *queries.Queries, t queries.LockSpendingTaskRow, provider, asked string) (agents.Model, error) {
	p, err := q.ModelPrices(ctx, t.OrgID, provider, asked)
	switch {
	case err == nil:
		return agents.Model{InputPrice: agents.USD(p.InputPrice), OutputPrice: agents.USD(p.OutputPrice)}, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return agents.Model{}, fmt.Errorf("pricing model %s of %s: %w", asked, provider, err)
	}
	return agents.Model{InputPrice: usd(t.InputPrice), OutputPrice: usd(t.OutputPrice)}, nil
}

// failTask ends a task that hasn't ended Failed, for reason, appends the change to its transcript, which no harness
// writes to any more, records its event in its agent's name and works out again the status of the branches at a
// review task's head; it answers whether the task had not ended.
func failTask(ctx context.Context, q *queries.Queries, taskID uuid.UUID, reason string) (bool, error) {
	r, err := q.FailTask(ctx, reason, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failing the task: %w", err)
	}
	last, err := q.LastEntry(ctx, taskID)
	if err != nil {
		return false, fmt.Errorf("reading the transcript's last entry: %w", err)
	}
	body, err := json.Marshal(agents.StateChange{State: agents.TaskFailed, Reason: reason})
	if err != nil {
		return false, fmt.Errorf("encoding the state entry: %w", err)
	}
	if _, err := q.InsertEntry(ctx, queries.InsertEntryParams{TaskID: taskID, Seq: last + 1, Kind: string(agents.EntryState), Body: body}); err != nil {
		return false, fmt.Errorf("appending the state entry: %w", err)
	}
	if err := orgEvent(ctx, q, &r.OrgID, r.AgentID, "task.failed", taskID, map[string]any{"number": r.Number}); err != nil {
		return false, err
	}
	return true, restatusReviewed(ctx, q, r.Kind, r.RepoID, r.Sha)
}
