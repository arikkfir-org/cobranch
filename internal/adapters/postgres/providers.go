package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
)

// storedProvider is the domain's view of an identity_providers row.
func storedProvider(row queries.IdentityProvider) auth.StoredProvider {
	out := auth.StoredProvider{ID: row.ID, Name: row.Name, ClientID: row.ClientID, SealedSecret: row.ClientSecret,
		Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if row.Issuer != nil {
		out.Issuer = *row.Issuer
	}
	if row.Preset != nil {
		out.Preset = *row.Preset
	}
	if row.UpdatedBy != nil {
		out.UpdatedBy = *row.UpdatedBy
	}
	return out
}

// readIdentityProviders lists the identity_providers rows with q, by ID.
func readIdentityProviders(ctx context.Context, q *queries.Queries) ([]auth.StoredProvider, error) {
	rows, err := q.IdentityProviders(ctx)
	if err != nil {
		return nil, wrap("listing the identity providers", err)
	}
	out := make([]auth.StoredProvider, 0, len(rows))
	for _, row := range rows {
		out = append(out, storedProvider(row))
	}
	return out, nil
}

// IdentityProviders lists the providers the Back-office added, for sign-in.
func (s *AuthStore) IdentityProviders(ctx context.Context) ([]auth.StoredProvider, error) {
	return readIdentityProviders(ctx, s.q)
}

// IdentityProviders lists the providers the Back-office added.
func (s *OrgStore) IdentityProviders(ctx context.Context) ([]auth.StoredProvider, error) {
	return readIdentityProviders(ctx, s.q)
}

// IdentityProvider finds the Back-office's provider id, or orgs.ErrNotFound.
func (s *OrgStore) IdentityProvider(ctx context.Context, id string) (auth.StoredProvider, error) {
	row, err := s.q.IdentityProvider(ctx, id)
	if err != nil {
		return auth.StoredProvider{}, wrapOrg("reading the identity provider", err)
	}
	return storedProvider(row), nil
}

// CreateIdentityProvider adds p, acting for actor, or admin.ErrProviderTaken for an ID another provider has.
func (s *OrgStore) CreateIdentityProvider(ctx context.Context, actor uuid.UUID, p auth.StoredProvider, at time.Time) (auth.StoredProvider, error) {
	params := queries.CreateIdentityProviderParams{ID: p.ID, Name: p.Name, Issuer: optional(p.Issuer), Preset: optional(p.Preset),
		ClientID: p.ClientID, ClientSecret: p.SealedSecret, UpdatedAt: at, UpdatedBy: &actor}
	var written auth.StoredProvider
	err := s.transact(ctx, "adding an identity provider", actor, func(ctx context.Context, q *queries.Queries) error {
		row, err := q.CreateIdentityProvider(ctx, params)
		if err != nil {
			return fmt.Errorf("writing it: %w", err)
		}
		written = storedProvider(row)
		return nil
	})
	if err != nil {
		return auth.StoredProvider{}, err
	}
	return written, nil
}

// ChangeIdentityProvider writes p, acting for actor, unless the row's version is no longer p.Version: then
// admin.ErrStale.
func (s *OrgStore) ChangeIdentityProvider(ctx context.Context, actor uuid.UUID, p auth.StoredProvider, at time.Time) (auth.StoredProvider, error) {
	params := queries.ChangeIdentityProviderParams{ID: p.ID, Name: p.Name, Issuer: optional(p.Issuer), Preset: optional(p.Preset),
		ClientID: p.ClientID, ClientSecret: p.SealedSecret, UpdatedAt: at, UpdatedBy: &actor, Version: p.Version}
	var written auth.StoredProvider
	err := s.transact(ctx, "changing an identity provider", actor, func(ctx context.Context, q *queries.Queries) error {
		row, err := q.ChangeIdentityProvider(ctx, params)
		if errors.Is(err, pgx.ErrNoRows) {
			return admin.ErrStale
		}
		if err != nil {
			return fmt.Errorf("writing it: %w", err)
		}
		written = storedProvider(row)
		return nil
	})
	if err != nil {
		return auth.StoredProvider{}, err
	}
	return written, nil
}

// DeleteIdentityProvider removes provider id, acting for actor, unless its version is no longer version: then
// admin.ErrStale.
func (s *OrgStore) DeleteIdentityProvider(ctx context.Context, actor uuid.UUID, id string, version int64) error {
	return s.transact(ctx, "removing an identity provider", actor, func(ctx context.Context, q *queries.Queries) error {
		removed, err := q.DeleteIdentityProvider(ctx, id, version)
		if err != nil {
			return fmt.Errorf("deleting it: %w", err)
		}
		if removed == 0 {
			return admin.ErrStale
		}
		return nil
	})
}

// optional is s, or nil when it is empty.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
