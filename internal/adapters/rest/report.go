package rest

import (
	"context"
	"time"

	"cobranch.dev/internal/services/agents"
)

// GetOrganizationSpend answers what the organization's agents spent in a month, in a time zone, this month in UTC by
// default, or 304 while the caller holds that answer.
func (a api) GetOrganizationSpend(ctx context.Context, req GetOrganizationSpendRequestObject) (GetOrganizationSpendResponseObject, error) {
	v, orgID, _, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	month, err := agents.MonthIn(orEmpty(req.Params.Month), orEmpty(req.Params.Zone), time.Now())
	if err != nil {
		return nil, fail(err, "organization")
	}
	report, err := a.agents.Spend(ctx, v, orgID, month.Since, month.Until)
	if err != nil {
		return nil, fail(err, "organization")
	}
	body := organizationSpend(month, report)
	tag, err := bodyTag("spend", body, nil)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetOrganizationSpend304Response{Headers: GetOrganizationSpend304ResponseHeaders{ETag: tag}}, nil
	}
	return GetOrganizationSpend200JSONResponse{Body: body, Headers: GetOrganizationSpend200ResponseHeaders{ETag: tag}}, nil
}

// organizationSpend is what the agents spent in month, as the API shows it.
func organizationSpend(month agents.Month, r agents.Report) OrganizationSpend {
	return OrganizationSpend{Month: month.Name, Zone: month.Zone, Since: r.Since.UTC(), Until: r.Until.UTC(),
		Agents: mapAll(r.Agents, func(s agents.AgentSpend) AgentSpend {
			out := AgentSpend{Agent: *agentPrincipal(&s.Agent), Provider: optional(s.Provider), Model: optional(s.Model), Cap: optionalUSD(s.Cap),
				Tasks: s.Tasks, FailedOnCap: s.FailedOnCap, Active: s.Active, Spent: usd(s.Spent)}
			if s.Costliest != nil {
				out.Costliest = &TaskCost{Number: s.Costliest.Number, Title: s.Costliest.Title, Spent: usd(s.Costliest.Spent)}
			}
			return out
		}),
		Models: mapAll(r.Models, func(m agents.ModelSpend) ModelSpend {
			return ModelSpend{Provider: m.Provider, Model: m.Model, Tasks: m.Tasks, InputTokens: m.Input, OutputTokens: m.Output, Spent: usd(m.Spent)}
		})}
}
