package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/limits"
)

// LimitStore reads the limits the database keeps and the usage it counts (db/migrations/00026_limits.sql):
// limits.Store on PostgreSQL. The transactions that would cross a limit count under its lock themselves, in the
// stores that run them.
type LimitStore struct {
	q *queries.Queries
}

// NewLimitStore returns the limits' store on pool's database.
func NewLimitStore(pool *pgxpool.Pool) *LimitStore {
	return &LimitStore{q: queries.New(pool)}
}

var _ limits.Store = (*LimitStore)(nil)

// InstallationLimits reads the limits the installation's settings set.
func (s *LimitStore) InstallationLimits(ctx context.Context) (limits.Settings, error) {
	row, err := s.q.InstallationLimits(ctx)
	if err != nil {
		return limits.Settings{}, fmt.Errorf("reading the installation's limits: %w", err)
	}
	return limits.Settings{OrganizationsPerUser: row.OrganizationsPerUser, RepositoriesPerOrganization: row.RepositoriesPerOrganization,
		GitDataPerOrganization: row.GitDataPerOrganization}, nil
}

// OrganizationLimits reads the organization's own limits; none for an organization without them.
func (s *LimitStore) OrganizationLimits(ctx context.Context, orgID uuid.UUID) (limits.Overrides, error) {
	row, err := s.q.OrganizationLimits(ctx, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return limits.Overrides{}, nil
	}
	if err != nil {
		return limits.Overrides{}, fmt.Errorf("reading the organization's limits: %w", err)
	}
	return limits.Overrides{Repositories: row.Repositories, GitData: row.GitData}, nil
}

// GitData sums the bytes of the organization's repositories' active packs.
func (s *LimitStore) GitData(ctx context.Context, orgID uuid.UUID) (int64, error) {
	used, err := s.q.GitData(ctx, orgID)
	if err != nil {
		return 0, fmt.Errorf("summing the organization's Git data: %w", err)
	}
	return used, nil
}

// lockLimit holds the subject's lock for limit until q's transaction ends.
func lockLimit(ctx context.Context, q *queries.Queries, limit limits.Kind, subject uuid.UUID) error {
	if err := q.LockLimit(ctx, string(limit), subject); err != nil {
		return fmt.Errorf("locking the %s limit: %w", limit, err)
	}
	return nil
}
