package rest

import (
	"context"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/orgs"
)

// apiKey is k as the API shows it.
func apiKey(k orgs.APIKey) ApiKey {
	out := ApiKey{Id: k.ID, OrgId: k.OrgID, Name: k.Name, CreatedAt: k.CreatedAt, Roles: nonNil(mapAll(k.Roles, role)), Secrets: nonNil(mapAll(k.Secrets, apiKeySecret))}
	if k.CreatedBy != uuid.Nil {
		out.CreatedBy = &k.CreatedBy
	}
	return out
}

func apiKeySecret(s orgs.Secret) ApiKeySecret {
	return ApiKeySecret{Id: s.ID, Hint: s.Hint, CreatedAt: s.CreatedAt, ExpiresAt: timeOrNil(s.ExpiresAt), LastUsedAt: timeOrNil(s.LastUsedAt), RevokedAt: timeOrNil(s.RevokedAt)}
}

// timeOrNil is t, or nil for the zero time, which the API leaves out.
func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// expiry is what the body says about a secret's end.
func expiry(expiresAt *time.Time, neverExpires *bool) orgs.Expiry {
	var e orgs.Expiry
	if expiresAt != nil {
		e.At = *expiresAt
	}
	if neverExpires != nil {
		e.Never = *neverExpires
	}
	return e
}

// ListAPIKeys lists the organization's API keys.
func (a api) ListAPIKeys(ctx context.Context, req ListAPIKeysRequestObject) (ListAPIKeysResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	keys, err := a.orgs.APIKeys(ctx, v, org.ID)
	if err != nil {
		return nil, fail(err, "organization")
	}
	return ListAPIKeys200JSONResponse{ApiKeys: nonNil(mapAll(keys, apiKey))}, nil
}

// CreateAPIKey makes an API key and shows its first secret once.
func (a api) CreateAPIKey(ctx context.Context, req CreateAPIKeyRequestObject) (CreateAPIKeyResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	in := orgs.APIKeyInput{Name: req.Body.Name, RoleIDs: req.Body.RoleIds, Expiry: expiry(req.Body.ExpiresAt, req.Body.NeverExpires)}
	key, secret, err := a.orgs.CreateAPIKey(ctx, v, org.ID, in)
	if err != nil {
		return nil, fail(err, "role")
	}
	return CreateAPIKey201JSONResponse{ApiKey: apiKey(key), Secret: secret}, nil
}

// RotateAPIKey gives the key a new secret and shows it once.
func (a api) RotateAPIKey(ctx context.Context, req RotateAPIKeyRequestObject) (RotateAPIKeyResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	var e orgs.Expiry
	if req.Body != nil {
		e = expiry(req.Body.ExpiresAt, req.Body.NeverExpires)
	}
	key, secret, err := a.orgs.RotateAPIKey(ctx, v, org.ID, req.KeyID, e)
	if err != nil {
		return nil, fail(err, "api_key")
	}
	return RotateAPIKey201JSONResponse{ApiKey: apiKey(key), Secret: secret}, nil
}

// RevokeAPIKeySecret revokes one of the key's secrets.
func (a api) RevokeAPIKeySecret(ctx context.Context, req RevokeAPIKeySecretRequestObject) (RevokeAPIKeySecretResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.orgs.RevokeSecret(ctx, v, org.ID, req.KeyID, req.SecretID); err != nil {
		return nil, fail(err, "secret")
	}
	return RevokeAPIKeySecret204Response{}, nil
}

// DeleteAPIKey deletes an API key with its secrets and grants.
func (a api) DeleteAPIKey(ctx context.Context, req DeleteAPIKeyRequestObject) (DeleteAPIKeyResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.orgs.DeleteAPIKey(ctx, v, org.ID, req.KeyID); err != nil {
		return nil, fail(err, "api_key")
	}
	return DeleteAPIKey204Response{}, nil
}
