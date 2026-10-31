package adminapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/admin"
)

// ListProviders answers the installation's identity providers, the values' first.
func (a api) ListProviders(ctx context.Context, _ ListProvidersRequestObject) (ListProvidersResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	listed, err := a.admin.Providers(ctx, v)
	if err != nil {
		return nil, fail(err, "provider")
	}
	items := make([]Provider, 0, len(listed))
	for _, p := range listed {
		items = append(items, provider(p))
	}
	return ListProviders200JSONResponse{Items: items}, nil
}

// GetProvider answers a provider with its ETag, or 304 while If-None-Match names it.
func (a api) GetProvider(ctx context.Context, req GetProviderRequestObject) (GetProviderResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	p, err := a.admin.Provider(ctx, v, req.Provider)
	if err != nil {
		return nil, fail(err, "provider")
	}
	tag := providerTag(p)
	if req.Params.IfNoneMatch != nil && matches(*req.Params.IfNoneMatch, tag) {
		return GetProvider304Response{Headers: GetProvider304ResponseHeaders{ETag: tag}}, nil
	}
	return GetProvider200JSONResponse{Body: provider(p), Headers: GetProvider200ResponseHeaders{ETag: tag}}, nil
}

// AddProvider adds a provider beside the values'.
func (a api) AddProvider(ctx context.Context, req AddProviderRequestObject) (AddProviderResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	p, err := a.admin.AddProvider(ctx, v, admin.NewProvider{ID: b.Id, Name: b.Name, Issuer: deref(b.Issuer),
		Preset: deref((*string)(b.Preset)), ClientID: b.ClientId, ClientSecret: b.ClientSecret})
	if err != nil {
		return nil, providerFail(err)
	}
	return AddProvider201JSONResponse{Body: provider(p), Headers: AddProvider201ResponseHeaders{ETag: providerTag(p),
		Location: BaseURL + "/providers/" + url.PathEscape(p.ID)}}, nil
}

// ChangeProvider changes what the body names of a provider, from the version If-Match names: 428 without it, 412
// once it changed.
func (a api) ChangeProvider(ctx context.Context, req ChangeProviderRequestObject) (ChangeProviderResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	match, err := ifMatch(req.Params.IfMatch, "the provider's")
	if err != nil {
		return nil, err
	}
	b := req.Body
	p, err := a.admin.ChangeProvider(ctx, v, req.Provider, match, admin.ProviderChange{Name: b.Name, Issuer: b.Issuer,
		Preset: (*string)(b.Preset), ClientID: b.ClientId, ClientSecret: b.ClientSecret})
	if err != nil {
		return nil, providerFail(err)
	}
	return ChangeProvider200JSONResponse{Body: provider(p), Headers: ChangeProvider200ResponseHeaders{ETag: providerTag(p)}}, nil
}

// RemoveProvider removes a provider, from the version If-Match names: 428 without it, 412 once it changed.
func (a api) RemoveProvider(ctx context.Context, req RemoveProviderRequestObject) (RemoveProviderResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	match, err := ifMatch(req.Params.IfMatch, "the provider's")
	if err != nil {
		return nil, err
	}
	if err := a.admin.RemoveProvider(ctx, v, req.Provider, match); err != nil {
		return nil, providerFail(err)
	}
	return RemoveProvider204Response{}, nil
}

// providerFail maps a provider change's refusals to problems, and the rest as fail does.
func providerFail(err error) error {
	switch {
	case errors.Is(err, admin.ErrStale):
		return refuse(http.StatusPreconditionFailed, "precondition.failed", "The provider changed since you read it: read it again.")
	case errors.Is(err, admin.ErrLocked):
		return refuse(http.StatusConflict, "provider.locked", "The chart's values give this provider: change it there.")
	case errors.Is(err, admin.ErrProviderTaken):
		return refuse(http.StatusConflict, "provider.taken", "Another provider has this ID.")
	case errors.Is(err, admin.ErrNoSecretsKey):
		return refuse(http.StatusConflict, "secrets_key.missing", "The installation has no secrets key to seal the client secret with: the operator sets one in the chart's values.")
	}
	return fail(err, "provider")
}

// providerTag is a provider's strong entity tag.
func providerTag(p admin.Provider) string {
	return `"` + p.Fingerprint() + `"`
}

func provider(p admin.Provider) Provider {
	out := Provider{Id: p.ID, Name: p.Name, ClientId: p.ClientID, Source: ProviderSource(p.Source), Shadowed: p.Shadowed,
		Issuer: optional(p.Issuer), Preset: optional(p.Preset)}
	if p.Source == admin.SourceBackOffice {
		created, updated := p.CreatedAt, p.UpdatedAt
		out.CreatedAt, out.UpdatedAt = &created, &updated
		if p.UpdatedBy != uuid.Nil {
			by := p.UpdatedBy
			out.UpdatedBy = &by
		}
	}
	return out
}

// optional is s, or nil when it is empty.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// deref is *s, or "" for nil.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
