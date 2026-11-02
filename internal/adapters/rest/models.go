package rest

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
)

// Models is what the API needs of the agents' service for the providers and models agents run on, and the secrets
// their tasks receive: part of Agents.
type Models interface {
	ProviderNames() []string
	ModelProviders(ctx context.Context, v agents.Viewer, orgID uuid.UUID) ([]agents.ModelProvider, error)
	ModelProvider(ctx context.Context, v agents.Viewer, orgID uuid.UUID, name string) (agents.ModelProvider, error)
	SetModelProvider(ctx context.Context, v agents.Viewer, orgID uuid.UUID, name string, secretID uuid.UUID, version int64) (agents.ModelProvider, bool, error)
	RemoveModelProvider(ctx context.Context, v agents.Viewer, orgID uuid.UUID, name string, version int64) error
	SetModel(ctx context.Context, v agents.Viewer, orgID, id uuid.UUID, provider, model string, version int64) (agents.Agent, error)
	SetSecrets(ctx context.Context, v agents.Viewer, orgID, id uuid.UUID, named map[string]uuid.UUID, version int64) (agents.Agent, error)
}

func modelProvider(p agents.ModelProvider) ModelProvider {
	return ModelProvider{Name: p.Name, SecretId: optionalID(p.SecretID), SecretName: optional(p.SecretName), Broken: p.Broken(),
		CreatedBy: agentPrincipal(p.CreatedBy), CreatedAt: p.CreatedAt.UTC(), UpdatedAt: p.UpdatedAt.UTC(), Version: p.Version}
}

// taggedProvider is p as the API shows it, with its ETag.
func taggedProvider(p agents.ModelProvider) (ModelProvider, string, error) {
	body := modelProvider(p)
	tag, err := versionTag(p.Version, body)
	return body, tag, err
}

// providerPath is the path of one of the organization's model providers from the API's base.
func providerPath(org, name string) string {
	return BaseURL + "/orgs/" + url.PathEscape(org) + "/model-providers/" + url.PathEscape(name)
}

// providerPrecondition is the version of a provider that a PUT names: If-Match's, or agents.NoVersion for
// If-None-Match *. It refuses with 428 a PUT that names neither, and with 400 one that names both or another
// If-None-Match.
func providerPrecondition(ifMatchHeader, ifNoneMatch *string) (int64, error) {
	none := ifNoneMatch != nil && strings.TrimSpace(*ifNoneMatch) != ""
	some := ifMatchHeader != nil && strings.TrimSpace(*ifMatchHeader) != ""
	switch {
	case none && some:
		return 0, refuse(http.StatusBadRequest, "precondition.invalid", "Name If-Match to change the provider or If-None-Match to add it, not both.")
	case none && strings.TrimSpace(*ifNoneMatch) != "*":
		return 0, refuse(http.StatusBadRequest, "precondition.invalid", "If-None-Match takes * alone: add the provider only if the organization hasn't.")
	case none:
		return agents.NoVersion, nil
	case !some:
		return 0, refuse(http.StatusPreconditionRequired, "precondition.required",
			"Name the provider's ETag in If-Match to change it, or If-None-Match: * to add it.")
	}
	return versionMatch(ifMatchHeader, agents.AnyVersion)
}

// ListModelProviders lists the providers the organization added, and those the installation knows.
func (a api) ListModelProviders(ctx context.Context, req ListModelProvidersRequestObject) (ListModelProvidersResponseObject, error) {
	v, orgID, _, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	list, err := a.agents.ModelProviders(ctx, v, orgID)
	if err != nil {
		return nil, fail(err, "organization")
	}
	return ListModelProviders200JSONResponse{ModelProviders: mapAll(list, modelProvider), Available: nonNil(a.agents.ProviderNames())}, nil
}

// GetModelProvider answers a provider the organization added.
func (a api) GetModelProvider(ctx context.Context, req GetModelProviderRequestObject) (GetModelProviderResponseObject, error) {
	v, orgID, _, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	p, err := a.agents.ModelProvider(ctx, v, orgID, req.Provider)
	if err != nil {
		return nil, fail(err, "model_provider")
	}
	body, tag, err := taggedProvider(p)
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetModelProvider304Response{Headers: GetModelProvider304ResponseHeaders{ETag: tag}}, nil
	}
	return GetModelProvider200JSONResponse{Body: body, Headers: GetModelProvider200ResponseHeaders{ETag: tag}}, nil
}

// SetModelProvider adds a provider to the organization with the secret its API key is in, or names another secret.
func (a api) SetModelProvider(ctx context.Context, req SetModelProviderRequestObject) (SetModelProviderResponseObject, error) {
	v, orgID, slug, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	version, err := providerPrecondition(req.Params.IfMatch, req.Params.IfNoneMatch)
	if err != nil {
		return nil, err
	}
	p, created, err := a.agents.SetModelProvider(ctx, v, orgID, req.Provider, req.Body.SecretId, version)
	if err != nil {
		return nil, fail(err, "model_provider")
	}
	body, tag, err := taggedProvider(p)
	if err != nil {
		return nil, err
	}
	if created {
		return SetModelProvider201JSONResponse{Body: body, Headers: SetModelProvider201ResponseHeaders{ETag: tag, Location: providerPath(slug, p.Name)}}, nil
	}
	return SetModelProvider200JSONResponse{Body: body, Headers: SetModelProvider200ResponseHeaders{ETag: tag}}, nil
}

// RemoveModelProvider removes a provider no agent runs on from the organization.
func (a api) RemoveModelProvider(ctx context.Context, req RemoveModelProviderRequestObject) (RemoveModelProviderResponseObject, error) {
	v, orgID, _, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	match, err := versionMatch(req.Params.IfMatch, agents.AnyVersion)
	if err != nil {
		return nil, err
	}
	if err := a.agents.RemoveModelProvider(ctx, v, orgID, req.Provider, match); err != nil {
		return nil, fail(err, "model_provider")
	}
	return RemoveModelProvider204Response{}, nil
}

// SetAgentModel puts an agent on a model of a provider the organization added, or on none.
func (a api) SetAgentModel(ctx context.Context, req SetAgentModelRequestObject) (SetAgentModelResponseObject, error) {
	v, orgID, _, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	match, err := versionMatch(req.Params.IfMatch, agents.AnyVersion)
	if err != nil {
		return nil, err
	}
	changed, err := a.agents.SetModel(ctx, v, orgID, req.AgentID, orEmpty(req.Body.Provider), orEmpty(req.Body.Model), match)
	if err != nil {
		return nil, fail(err, "agent")
	}
	body, tag, err := taggedAgent(changed)
	if err != nil {
		return nil, err
	}
	return SetAgentModel200JSONResponse{Body: body, Headers: SetAgentModel200ResponseHeaders{ETag: tag}}, nil
}

// SetAgentSecrets names the secrets an agent's tasks receive.
func (a api) SetAgentSecrets(ctx context.Context, req SetAgentSecretsRequestObject) (SetAgentSecretsResponseObject, error) {
	v, orgID, _, err := a.agentOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	match, err := versionMatch(req.Params.IfMatch, agents.AnyVersion)
	if err != nil {
		return nil, err
	}
	changed, err := a.agents.SetSecrets(ctx, v, orgID, req.AgentID, req.Body.Secrets, match)
	if err != nil {
		return nil, fail(err, "agent")
	}
	body, tag, err := taggedAgent(changed)
	if err != nil {
		return nil, err
	}
	return SetAgentSecrets200JSONResponse{Body: body, Headers: SetAgentSecrets200ResponseHeaders{ETag: tag}}, nil
}
