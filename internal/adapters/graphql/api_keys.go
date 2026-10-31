package graphql

import (
	"context"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/orgs"
)

// apiKey is k as the API shows it.
func apiKey(k orgs.APIKey) *APIKey {
	out := &APIKey{ID: k.ID.String(), OrganizationID: k.OrgID.String(), Name: k.Name, CreatedAt: k.CreatedAt,
		Roles: nonNil(mapAll(k.Roles, role)), Secrets: nonNil(mapAll(k.Secrets, apiKeySecret))}
	if k.CreatedBy != uuid.Nil {
		by := k.CreatedBy.String()
		out.CreatedBy = &by
	}
	return out
}

func apiKeySecret(s orgs.Secret) *APIKeySecret {
	return &APIKeySecret{ID: s.ID.String(), Hint: s.Hint, CreatedAt: s.CreatedAt,
		ExpiresAt: timeOrNil(s.ExpiresAt), LastUsedAt: timeOrNil(s.LastUsedAt), RevokedAt: timeOrNil(s.RevokedAt)}
}

// timeOrNil is t, or nil for the zero time, which the API shows as null.
func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// expiry is what the input says about a secret's end.
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

// apiKeyInput is input as the domain takes it; a role ID that isn't one is a role not found.
func apiKeyInput(ctx context.Context, input APIKeyInput) (orgs.APIKeyInput, error) {
	in := orgs.APIKeyInput{Name: input.Name, Expiry: expiry(input.ExpiresAt, input.NeverExpires)}
	for _, id := range input.RoleIds {
		roleID, err := parseID(ctx, id, "role")
		if err != nil {
			return orgs.APIKeyInput{}, err
		}
		in.RoleIDs = append(in.RoleIDs, roleID)
	}
	return in, nil
}
