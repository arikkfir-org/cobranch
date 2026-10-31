package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// credential is the domain's view of a credentials row.
func credential(c queries.Credential) auth.Credential {
	out := auth.Credential{ID: c.ID, Kind: auth.CredentialKind(c.Kind), PrincipalID: c.PrincipalID, Hint: c.Hint, CreatedAt: c.CreatedAt,
		Name: c.Name, Permissions: c.Permissions, Issuer: c.Issuer, Subject: c.Subject}
	out.CreatedBy = orNil(c.CreatedBy)
	out.ExpiresAt, out.LastUsedAt, out.RevokedAt = orZero(c.ExpiresAt), orZero(c.LastUsedAt), orZero(c.RevokedAt)
	return out
}

// orNil is the ID p points to, or uuid.Nil.
func orNil(p *uuid.UUID) uuid.UUID {
	if p == nil {
		return uuid.Nil
	}
	return *p
}

// orZero is the time p points to, or the zero time.
func orZero(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

// nilIfZero is t as a nullable column: null for the zero time.
func nilIfZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// Credential finds a credential by its token's hash, with the principal it acts as: an API key, or a user with their
// emails.
func (s *AuthStore) Credential(ctx context.Context, tokenHash []byte) (auth.Credential, auth.User, error) {
	row, err := s.q.CredentialByToken(ctx, tokenHash)
	if err != nil {
		return auth.Credential{}, auth.User{}, wrap("reading the credential", err)
	}
	u, err := s.user(ctx, row.Principal)
	if err != nil {
		return auth.Credential{}, auth.User{}, err
	}
	return credential(row.Credential), u, nil
}

// TouchCredential records that credential id was used at usedAt.
func (s *AuthStore) TouchCredential(ctx context.Context, id uuid.UUID, usedAt time.Time) error {
	if err := s.q.TouchCredential(ctx, usedAt, id); err != nil {
		return fmt.Errorf("marking the credential used: %w", err)
	}
	return nil
}

// secret is the key manager's view of a credentials row.
func secret(c queries.Credential) orgs.Secret {
	return orgs.Secret{ID: c.ID, Hint: c.Hint, CreatedAt: c.CreatedAt,
		ExpiresAt: orZero(c.ExpiresAt), LastUsedAt: orZero(c.LastUsedAt), RevokedAt: orZero(c.RevokedAt)}
}

// apiKey is the domain's view of an api_key principal, with the roles and secrets given.
func apiKey(p queries.Principal, roles []orgs.Role, secrets []orgs.Secret) orgs.APIKey {
	return orgs.APIKey{ID: p.ID, OrgID: orNil(p.OrgID), Name: p.DisplayName, CreatedBy: orNil(p.CreatedBy), CreatedAt: p.CreatedAt,
		Roles: roles, Secrets: secrets}
}

// secretsHeld lists the secrets of each of the organization's keys, oldest first.
func (s *OrgStore) secretsHeld(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID][]orgs.Secret, error) {
	rows, err := s.q.OrganizationCredentials(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing the keys' secrets: %w", err)
	}
	held := map[uuid.UUID][]orgs.Secret{}
	for _, c := range rows {
		held[c.PrincipalID] = append(held[c.PrincipalID], secret(c))
	}
	return held, nil
}

// APIKeys lists the organization's API keys with their roles and secrets, by name.
func (s *OrgStore) APIKeys(ctx context.Context, orgID uuid.UUID) ([]orgs.APIKey, error) {
	rows, err := s.q.APIKeys(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing the API keys: %w", err)
	}
	roles, err := s.rolesHeld(ctx, orgID)
	if err != nil {
		return nil, err
	}
	secrets, err := s.secretsHeld(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]orgs.APIKey, 0, len(rows))
	for _, p := range rows {
		out = append(out, apiKey(p, roles[p.ID], secrets[p.ID]))
	}
	return out, nil
}

// APIKey finds one of the organization's API keys with its roles and secrets.
func (s *OrgStore) APIKey(ctx context.Context, orgID, id uuid.UUID) (orgs.APIKey, error) {
	p, err := s.q.APIKey(ctx, id, orgID)
	if err != nil {
		return orgs.APIKey{}, wrapOrg("reading the API key", err)
	}
	roles, err := s.rolesHeld(ctx, orgID)
	if err != nil {
		return orgs.APIKey{}, err
	}
	secrets, err := s.secretsHeld(ctx, orgID)
	if err != nil {
		return orgs.APIKey{}, err
	}
	return apiKey(p, roles[p.ID], secrets[p.ID]), nil
}

// insertSecret records a secret of the key, hashed.
func insertSecret(ctx context.Context, q *queries.Queries, actor, keyID uuid.UUID, sec orgs.Secret, hash []byte) error {
	if err := q.InsertCredential(ctx, queries.InsertCredentialParams{
		ID: sec.ID, TokenHash: hash, PrincipalID: keyID, Hint: sec.Hint, CreatedAt: sec.CreatedAt, CreatedBy: &actor, ExpiresAt: nilIfZero(sec.ExpiresAt),
	}); err != nil {
		return fmt.Errorf("inserting the secret: %w", err)
	}
	return nil
}

// CreateAPIKey makes the key's principal, grants it its roles at the organization and records its first secret and
// the event, under the organization's lock, so a role being deleted meanwhile isn't granted.
func (s *OrgStore) CreateAPIKey(ctx context.Context, actor uuid.UUID, key orgs.APIKey, sec orgs.Secret, hash []byte) error {
	return s.transact(ctx, "recording the API key", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.LockOrganization(ctx, key.OrgID); err != nil {
			return wrapOrg("locking the organization", err)
		}
		if err := q.InsertAPIKey(ctx, queries.InsertAPIKeyParams{
			ID: key.ID, Name: key.Name, CreatedAt: key.CreatedAt, OrgID: &key.OrgID, CreatedBy: &actor,
		}); err != nil {
			return fmt.Errorf("inserting the key's principal: %w", err)
		}
		roles := make([]string, 0, len(key.Roles))
		for _, r := range key.Roles {
			if _, err := q.Role(ctx, r.ID); err != nil {
				return wrapOrg("reading the role", err)
			}
			if err := grantRole(ctx, q, key.ID, r.ID, key.OrgID); err != nil {
				return err
			}
			roles = append(roles, r.Name)
		}
		if err := insertSecret(ctx, q, actor, key.ID, sec, hash); err != nil {
			return err
		}
		return orgEvent(ctx, q, &key.OrgID, actor, "api_key.created", key.ID, map[string]any{"name": key.Name, "roles": roles, "secret": sec.ID})
	})
}

// lockedKey locks the organization and finds its key, for a change to the key.
func lockedKey(ctx context.Context, q *queries.Queries, orgID, keyID uuid.UUID) (queries.Principal, error) {
	if _, err := q.LockOrganization(ctx, orgID); err != nil {
		return queries.Principal{}, wrapOrg("locking the organization", err)
	}
	p, err := q.APIKey(ctx, keyID, orgID)
	if err != nil {
		return queries.Principal{}, wrapOrg("reading the API key", err)
	}
	return p, nil
}

// AddSecret gives the key another secret, unless two act at `at`: ErrTwoSecrets, decided under the organization's
// lock, so two rotations at once don't make a third.
func (s *OrgStore) AddSecret(ctx context.Context, actor uuid.UUID, orgID, keyID uuid.UUID, sec orgs.Secret, hash []byte, at time.Time) error {
	return s.transact(ctx, "adding the secret", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := lockedKey(ctx, q, orgID, keyID); err != nil {
			return err
		}
		live, err := q.CountLiveCredentials(ctx, keyID, at)
		if err != nil {
			return fmt.Errorf("counting the live secrets: %w", err)
		}
		if live >= 2 {
			return orgs.ErrTwoSecrets
		}
		if err := insertSecret(ctx, q, actor, keyID, sec, hash); err != nil {
			return err
		}
		return orgEvent(ctx, q, &orgID, actor, "api_key.rotated", keyID, map[string]any{"secret": sec.ID, "hint": sec.Hint})
	})
}

// RevokeSecret revokes one of the key's secrets at `at`.
func (s *OrgStore) RevokeSecret(ctx context.Context, actor uuid.UUID, orgID, keyID, secretID uuid.UUID, at time.Time) error {
	return s.transact(ctx, "revoking the secret", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := lockedKey(ctx, q, orgID, keyID); err != nil {
			return err
		}
		revoked, err := q.RevokeCredential(ctx, at, secretID, keyID)
		if err != nil {
			return fmt.Errorf("updating the secret: %w", err)
		}
		if revoked == 0 {
			return orgs.ErrNotFound
		}
		return orgEvent(ctx, q, &orgID, actor, "api_key.secret_revoked", keyID, map[string]any{"secret": secretID})
	})
}

// DeleteAPIKey deletes the key's principal, and with it its secrets and grants; the event keeps its name.
func (s *OrgStore) DeleteAPIKey(ctx context.Context, actor uuid.UUID, orgID, keyID uuid.UUID) error {
	return s.transact(ctx, "deleting the API key", actor, func(ctx context.Context, q *queries.Queries) error {
		p, err := lockedKey(ctx, q, orgID, keyID)
		if err != nil {
			return err
		}
		if _, err := q.DeleteAPIKey(ctx, keyID, orgID); err != nil {
			return fmt.Errorf("deleting the key's principal: %w", err)
		}
		return orgEvent(ctx, q, &orgID, actor, "api_key.deleted", keyID, map[string]any{"name": p.DisplayName})
	})
}

// grantRole gives the principal the role at the organization, once.
func grantRole(ctx context.Context, q *queries.Queries, principalID, roleID, orgID uuid.UUID) error {
	if _, err := q.InsertGrant(ctx, queries.InsertGrantParams{
		PrincipalID: principalID, RoleID: roleID, Scope: string(auth.ScopeOrganization), ScopeID: &orgID, Source: string(auth.SourceGrant),
	}); err != nil {
		return fmt.Errorf("inserting the grant: %w", err)
	}
	return nil
}
