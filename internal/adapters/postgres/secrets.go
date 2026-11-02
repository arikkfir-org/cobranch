package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/secrets"
)

// SecretStore keeps the organizations' and repositories' secrets (db/migrations/00030_secrets.sql): secrets.Store on
// PostgreSQL. It reads a secret's sealed value only in Sealed, and writes it only as it creates or replaces one.
type SecretStore struct {
	pool *pgxpool.Pool
	q    *queries.Queries
}

// NewSecretStore returns secrets' store on pool's database.
func NewSecretStore(pool *pgxpool.Pool) *SecretStore {
	return &SecretStore{pool: pool, q: queries.New(pool)}
}

var _ secrets.Store = (*SecretStore)(nil)

// transact runs fn in a transaction that acts for actor (transact), mapping a taken name to secrets.ErrNameTaken.
func (s *SecretStore) transact(ctx context.Context, what string, actor uuid.UUID, fn func(ctx context.Context, q *queries.Queries) error) error {
	err := transact(ctx, s.pool, s.q, what, actor, fn)
	var conflict *ConflictError
	if errors.As(err, &conflict) && conflict.Constraint == "secrets_org_id_repo_id_name_key" {
		return secrets.ErrNameTaken
	}
	return err
}

// wrapSecret returns secrets.ErrNotFound for a query that found no row, and err in what's context otherwise.
func wrapSecret(what string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return secrets.ErrNotFound
	}
	return fmt.Errorf("%s: %w", what, err)
}

// secretOf is the domain's view of a secrets row, which every query reading one answers in the same columns.
func secretOf(r queries.SecretRow) secrets.Secret {
	out := secrets.Secret{ID: r.ID, OrgID: r.OrgID, Name: r.Name, Version: r.Version, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	for to, from := range map[*uuid.UUID]*uuid.UUID{&out.RepoID: r.RepoID, &out.CreatedBy: r.CreatedBy, &out.UpdatedBy: r.UpdatedBy} {
		if from != nil {
			*to = *from
		}
	}
	return out
}

// nullable is id, or nil for uuid.Nil.
func nullable(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// List lists the secrets of the organization, or of one of its repositories, by name.
func (s *SecretStore) List(ctx context.Context, orgID, repoID uuid.UUID) ([]secrets.Secret, error) {
	rows, err := s.q.Secrets(ctx, orgID, nullable(repoID))
	if err != nil {
		return nil, fmt.Errorf("listing the secrets: %w", err)
	}
	out := make([]secrets.Secret, 0, len(rows))
	for _, r := range rows {
		out = append(out, secretOf(queries.SecretRow(r)))
	}
	return out, nil
}

// Get finds a secret.
func (s *SecretStore) Get(ctx context.Context, id uuid.UUID) (secrets.Secret, error) {
	r, err := s.q.Secret(ctx, id)
	if err != nil {
		return secrets.Secret{}, wrapSecret("reading the secret", err)
	}
	return secretOf(r), nil
}

// Create writes the secret with its sealed value and records the event.
func (s *SecretStore) Create(ctx context.Context, actor uuid.UUID, secret secrets.Secret, sealed []byte) (secrets.Secret, error) {
	var out secrets.Secret
	err := s.transact(ctx, "creating the secret", actor, func(ctx context.Context, q *queries.Queries) error {
		r, err := q.InsertSecret(ctx, queries.InsertSecretParams{ID: secret.ID, OrgID: secret.OrgID, RepoID: nullable(secret.RepoID),
			Name: secret.Name, Value: sealed, Actor: actor})
		if err != nil {
			return wrapSecret("inserting the secret", err)
		}
		out = secretOf(queries.SecretRow(r))
		return orgEvent(ctx, q, &secret.OrgID, actor, "secret.created", secret.ID, map[string]any{"name": secret.Name, "repo": nullable(secret.RepoID)})
	})
	return out, err
}

// current locks the secret and checks it is at version (any with AnyVersion).
func current(ctx context.Context, q *queries.Queries, id uuid.UUID, version int64) (secrets.Secret, error) {
	r, err := q.SecretForUpdate(ctx, id)
	if err != nil {
		return secrets.Secret{}, wrapSecret("locking the secret", err)
	}
	if version != secrets.AnyVersion && r.Version != version {
		return secrets.Secret{}, secrets.ErrChanged
	}
	return secretOf(queries.SecretRow(r)), nil
}

// Replace renames the secret, and replaces its value unless sealed is nil, if it is still at version.
func (s *SecretStore) Replace(ctx context.Context, actor uuid.UUID, id uuid.UUID, version int64, name string, sealed []byte) (secrets.Secret, error) {
	var out secrets.Secret
	err := s.transact(ctx, "replacing the secret", actor, func(ctx context.Context, q *queries.Queries) error {
		before, err := current(ctx, q, id, version)
		if err != nil {
			return err
		}
		r, err := q.ReplaceSecret(ctx, queries.ReplaceSecretParams{ID: id, Name: name, Value: sealed, Actor: actor})
		if err != nil {
			return wrapSecret("updating the secret", err)
		}
		out = secretOf(queries.SecretRow(r))
		return orgEvent(ctx, q, &before.OrgID, actor, "secret.replaced", id, map[string]any{"name": name, "value": sealed != nil})
	})
	return out, err
}

// Delete deletes the secret, and with it the grants on it, if it is still at version.
func (s *SecretStore) Delete(ctx context.Context, actor uuid.UUID, id uuid.UUID, version int64) error {
	return s.transact(ctx, "deleting the secret", actor, func(ctx context.Context, q *queries.Queries) error {
		before, err := current(ctx, q, id, version)
		if err != nil {
			return err
		}
		if err := q.DeleteSecret(ctx, id); err != nil {
			return fmt.Errorf("deleting the secret: %w", err)
		}
		return orgEvent(ctx, q, &before.OrgID, actor, "secret.deleted", id, map[string]any{"name": before.Name})
	})
}

// Sealed reads the sealed values of those of ids that are there.
func (s *SecretStore) Sealed(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]byte, error) {
	rows, err := s.q.SealedSecrets(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("reading the sealed values: %w", err)
	}
	out := make(map[uuid.UUID][]byte, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Value
	}
	return out, nil
}

// Access lists the principals holding a grant on the secret, by display name.
func (s *SecretStore) Access(ctx context.Context, id uuid.UUID) ([]secrets.Principal, error) {
	rows, err := s.q.SecretGrantees(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("listing the secret's grants: %w", err)
	}
	out := make([]secrets.Principal, 0, len(rows))
	for _, r := range rows {
		out = append(out, secrets.Principal{ID: r.ID, Kind: auth.Kind(r.Kind), DisplayName: r.DisplayName})
	}
	return out, nil
}

// Jobs lists the jobs of the organization's repository, by name.
func (s *SecretStore) Jobs(ctx context.Context, orgID, repoID uuid.UUID) ([]secrets.Principal, error) {
	rows, err := s.q.RepositoryJobs(ctx, orgID, repoID)
	if err != nil {
		return nil, fmt.Errorf("listing the jobs: %w", err)
	}
	out := make([]secrets.Principal, 0, len(rows))
	for _, r := range rows {
		out = append(out, secrets.Principal{ID: r.ID, Kind: auth.KindJob, DisplayName: r.Name})
	}
	return out, nil
}

// Principal finds one of the organization's members, or a principal of its own: an API key, a job or an agent.
func (s *SecretStore) Principal(ctx context.Context, orgID, principalID uuid.UUID) (secrets.Principal, error) {
	r, err := s.q.Grantee(ctx, principalID, orgID)
	if err != nil {
		return secrets.Principal{}, wrapSecret("finding the principal", err)
	}
	return secrets.Principal{ID: r.ID, Kind: auth.Kind(r.Kind), DisplayName: r.DisplayName}, nil
}

// Grant gives the principal Secret reader on the secret and records it.
func (s *SecretStore) Grant(ctx context.Context, actor uuid.UUID, secret secrets.Secret, principalID uuid.UUID) error {
	return s.transact(ctx, "granting Secret reader", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.LockOrganization(ctx, secret.OrgID); err != nil {
			return wrapSecret("locking the organization", err)
		}
		if _, err := current(ctx, q, secret.ID, secrets.AnyVersion); err != nil {
			return err
		}
		granted, err := q.InsertGrant(ctx, queries.InsertGrantParams{
			PrincipalID: principalID, RoleID: auth.RoleSecretReader, Scope: string(auth.ScopeSecret), ScopeID: &secret.ID, Source: string(auth.SourceGrant),
		})
		if err != nil {
			return fmt.Errorf("inserting the grant: %w", err)
		}
		if granted == 0 {
			return secrets.ErrGranted
		}
		return orgEvent(ctx, q, &secret.OrgID, actor, "secret.granted", principalID, map[string]any{"secret": secret.ID, "role": auth.RoleSecretReader})
	})
}

// Revoke takes Secret reader on the secret from the principal and records it.
func (s *SecretStore) Revoke(ctx context.Context, actor uuid.UUID, secret secrets.Secret, principalID uuid.UUID) error {
	return s.transact(ctx, "revoking Secret reader", actor, func(ctx context.Context, q *queries.Queries) error {
		n, err := q.DeleteSecretGrant(ctx, principalID, auth.RoleSecretReader, secret.ID)
		if err != nil {
			return fmt.Errorf("deleting the grant: %w", err)
		}
		if n == 0 {
			return secrets.ErrNotFound
		}
		return orgEvent(ctx, q, &secret.OrgID, actor, "secret.revoked", principalID, map[string]any{"secret": secret.ID, "role": auth.RoleSecretReader})
	})
}
