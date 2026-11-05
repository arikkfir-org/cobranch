package rest

import (
	"context"
	"net/http"
	"net/url"

	"cobranch.dev/internal/services/agents"
)

// usd is an amount as the API shows it, such as 8.00.
func usd(u agents.USD) string { return u.String() }

// optionalUSD is an amount as the API shows it, or nil for 0, which is none.
func optionalUSD(u agents.USD) *string {
	if u == 0 {
		return nil
	}
	return new(u.String())
}

// parseUSD reads the amount a request names for field, refusing one that isn't with 400 code.
func parseUSD(s, field, code string) (agents.USD, error) {
	u, err := agents.ParseUSD(s)
	if err != nil {
		return 0, refuse(http.StatusBadRequest, code, "The "+field+" is an amount of US dollars such as 8.00, with at most six digits after the point.")
	}
	return u, nil
}

func model(m agents.Model) Model {
	out := Model{Provider: m.Provider, Name: m.Name, InputPrice: usd(m.InputPrice), OutputPrice: usd(m.OutputPrice), Cap: optionalUSD(m.Cap),
		Default: m.Default, Agents: make([]Principal, 0, len(m.Agents)), CreatedBy: agentPrincipal(m.CreatedBy), CreatedAt: m.CreatedAt.UTC(),
		UpdatedAt: m.UpdatedAt.UTC(), Version: m.Version}
	for _, a := range m.Agents {
		out.Agents = append(out.Agents, *agentPrincipal(&a))
	}
	return out
}

// taggedModel is m as the API shows it, with its ETag.
func taggedModel(m agents.Model) (Model, string, error) {
	body := model(m)
	tag, err := versionTag(m.Version, body)
	return body, tag, err
}

// modelPath is the path of one of the organization's models from the API's base.
func modelPath(org, provider, name string) string {
	return BaseURL + "/orgs/" + url.PathEscape(org) + "/models/" + url.PathEscape(provider) + "/" + url.PathEscape(name)
}

// modelInput is what a PUT sets a model to.
func modelInput(b ModelInput) (agents.ModelInput, error) {
	in := agents.ModelInput{Default: b.Default != nil && *b.Default}
	var err error
	if in.InputPrice, err = parseUSD(b.InputPrice, "input price", "model.price_invalid"); err != nil {
		return in, err
	}
	if in.OutputPrice, err = parseUSD(b.OutputPrice, "output price", "model.price_invalid"); err != nil {
		return in, err
	}
	if b.Cap != nil {
		in.Cap, err = parseUSD(*b.Cap, "cap", "model.cap_invalid")
	}
	return in, err
}

// ListModels lists the organization's models, each with the agents on it.
func (a api) ListModels(ctx context.Context, req ListModelsRequestObject) (ListModelsResponseObject, error) {
	v, orgID, _, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	list, err := a.agents.Models(ctx, v, orgID)
	if err != nil {
		return nil, fail(err, "organization")
	}
	return ListModels200JSONResponse{Models: mapAll(list, model)}, nil
}

// GetModel answers one of the organization's models.
func (a api) GetModel(ctx context.Context, req GetModelRequestObject) (GetModelResponseObject, error) {
	v, orgID, _, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	m, err := a.agents.Model(ctx, v, orgID, req.Provider, req.Model)
	if err != nil {
		return nil, fail(err, "model")
	}
	body, tag, err := taggedModel(m)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetModel304Response{Headers: GetModel304ResponseHeaders{ETag: tag}}, nil
	}
	return GetModel200JSONResponse{Body: body, Headers: GetModel200ResponseHeaders{ETag: tag}}, nil
}

// SetModel adds a model to the organization's, or changes one of them.
func (a api) SetModel(ctx context.Context, req SetModelRequestObject) (SetModelResponseObject, error) {
	v, orgID, slug, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	version, err := putPrecondition(req.Params.IfMatch, req.Params.IfNoneMatch, "model")
	if err != nil {
		return nil, err
	}
	in, err := modelInput(*req.Body)
	if err != nil {
		return nil, err
	}
	m, created, err := a.agents.PutModel(ctx, v, orgID, req.Provider, req.Model, in, version)
	if err != nil {
		return nil, fail(err, "model")
	}
	body, tag, err := taggedModel(m)
	if err != nil {
		return nil, err
	}
	if created {
		return SetModel201JSONResponse{Body: body, Headers: SetModel201ResponseHeaders{ETag: tag, Location: modelPath(slug, m.Provider, m.Name)}}, nil
	}
	return SetModel200JSONResponse{Body: body, Headers: SetModel200ResponseHeaders{ETag: tag}}, nil
}

// RemoveModel removes one of the organization's models that no agent runs on.
func (a api) RemoveModel(ctx context.Context, req RemoveModelRequestObject) (RemoveModelResponseObject, error) {
	v, orgID, _, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	match, err := versionMatch(req.Params.IfMatch, agents.AnyVersion)
	if err != nil {
		return nil, err
	}
	if err := a.agents.RemoveModel(ctx, v, orgID, req.Provider, req.Model, match); err != nil {
		return nil, fail(err, "model")
	}
	return RemoveModel204Response{}, nil
}

// RecordMyTaskSpend records a call of the model of the task the request's task token works, and answers what the task
// spent.
func (a api) RecordMyTaskSpend(ctx context.Context, req RecordMyTaskSpendRequestObject) (RecordMyTaskSpendResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	spend, err := a.agents.RecordSpend(ctx, v, agents.Usage{ID: b.Id, Model: b.Model, Input: b.InputTokens, Output: b.OutputTokens})
	if err != nil {
		return nil, fail(err, "task")
	}
	return RecordMyTaskSpend200JSONResponse{Spent: usd(spend.Spent), InputTokens: spend.Input, OutputTokens: spend.Output, Cap: optionalUSD(spend.Cap),
		Ended: spend.Ended}, nil
}
