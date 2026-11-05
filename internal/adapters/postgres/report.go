package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/agents"
)

// Spend reports what the organization's agents spent from since until until, naming as an agent's costliest task only
// one the viewer v describes sees (agents.Store).
func (s *AgentStore) Spend(ctx context.Context, orgID uuid.UUID, since, until time.Time, v agents.Shown) (agents.Report, error) {
	rows, err := s.q.AgentSpend(ctx, queries.AgentSpendParams{OrgID: orgID, Readable: v.Readable, Hidden: v.Hidden, PrivateFor: v.PrivateFor,
		Since: since, Until: until})
	if err != nil {
		return agents.Report{}, fmt.Errorf("summing the agents' spend: %w", err)
	}
	models, err := s.q.ModelSpend(ctx, orgID, since, until)
	if err != nil {
		return agents.Report{}, fmt.Errorf("summing the models' spend: %w", err)
	}
	out := agents.Report{Since: since, Until: until, Agents: make([]agents.AgentSpend, 0, len(rows)), Models: make([]agents.ModelSpend, 0, len(models))}
	for _, r := range rows {
		out.Agents = append(out.Agents, agentSpend(r))
	}
	for _, m := range models {
		out.Models = append(out.Models, agents.ModelSpend{Provider: m.Provider, Model: m.Model, Tasks: m.Tasks, Input: m.InputTokens,
			Output: m.OutputTokens, Spent: agents.USD(m.Spent)})
	}
	return out, nil
}

// agentSpend is the domain's view of what an agent spent in a report's span.
func agentSpend(r queries.AgentSpendRow) agents.AgentSpend {
	a := agents.AgentSpend{Agent: agents.Principal{ID: r.PrincipalID, DisplayName: r.Name}, Cap: agents.USD(r.Cap), Tasks: r.Tasks,
		FailedOnCap: r.FailedOnCap, Active: r.Active, Spent: agents.USD(r.Spent)}
	if r.Provider != nil && r.Model != nil {
		a.Provider, a.Model = *r.Provider, *r.Model
	}
	if r.CostliestNumber > 0 {
		a.Costliest = &agents.TaskCost{Number: r.CostliestNumber, Title: r.CostliestTitle, Spent: agents.USD(r.CostliestSpent)}
	}
	return a
}
