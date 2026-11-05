package graphql

import (
	"context"

	"github.com/99designs/gqlgen/graphql"
	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
)

// optionalUSD is an amount as the API shows it, such as 8.00, or nil for 0, which is none.
func optionalUSD(u agents.USD) *string {
	if u == 0 {
		return nil
	}
	return new(u.String())
}

// parseUSD reads the amount an input names, or a BAD_USER_INPUT error of code for one that isn't.
func parseUSD(ctx context.Context, s, code string) (agents.USD, error) {
	u, err := agents.ParseUSD(s)
	if err != nil {
		return 0, refuse(ctx, badUserInput, code, "an amount of US dollars such as 8.00, with at most six digits after the point")
	}
	return u, nil
}

// modelOf is m as the API shows it; SetModel's model parameter shadows a function named model.
func modelOf(m agents.Model) *Model {
	out := &Model{Provider: m.Provider, Name: m.Name, InputPrice: m.InputPrice.String(), OutputPrice: m.OutputPrice.String(),
		Default: m.Default, Agents: make([]*CommitPrincipal, 0, len(m.Agents)), CreatedBy: agentPrincipal(m.CreatedBy), CreatedAt: m.CreatedAt.UTC(),
		UpdatedAt: m.UpdatedAt.UTC(), Version: int(m.Version)}
	for _, a := range m.Agents {
		out.Agents = append(out.Agents, agentPrincipal(&a))
	}
	return out
}

// modelInput is in as the domain takes it, or a BAD_USER_INPUT error for an amount that isn't one.
func modelInput(ctx context.Context, in ModelInput) (agents.ModelInput, error) {
	out := agents.ModelInput{Default: in.Default != nil && *in.Default}
	var err error
	if out.InputPrice, err = parseUSD(ctx, in.InputPrice, "model.price_invalid"); err != nil {
		return out, err
	}
	out.OutputPrice, err = parseUSD(ctx, in.OutputPrice, "model.price_invalid")
	return out, err
}

// capOrClear is the cap an omittable field names, 0 for null, or nil when it is left out.
func capOrClear(ctx context.Context, field graphql.Omittable[*string]) (*agents.USD, error) {
	value, set := field.ValueOK()
	switch {
	case !set:
		return nil, nil
	case value == nil:
		return new(agents.USD(0)), nil
	}
	u, err := parseUSD(ctx, *value, "agent.cap_invalid")
	return &u, err
}

// usage is a reported call as the domain takes it, or a BAD_USER_INPUT error for an ID that isn't one.
func usage(ctx context.Context, in TaskUsageInput) (agents.Usage, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return agents.Usage{}, refuse(ctx, badUserInput, "spend.id_invalid", "not an ID")
	}
	return agents.Usage{ID: id, Model: in.Model, Input: int64(in.InputTokens), Output: int64(in.OutputTokens)}, nil
}
