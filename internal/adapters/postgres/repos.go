package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// RepoStore keeps repositories' rows (db/migrations/00010_repositories.sql): repos.Store on PostgreSQL, through the
// queries sqlc generates from db/queries.
type RepoStore struct {
	pool *pgxpool.Pool
	q    *queries.Queries
}

// NewRepoStore returns repositories' store on pool's database.
func NewRepoStore(pool *pgxpool.Pool) *RepoStore {
	return &RepoStore{pool: pool, q: queries.New(pool)}
}

var _ repos.Store = (*RepoStore)(nil)

// repoConflicts maps the constraints a unique violation names to the domain's errors.
var repoConflicts = map[string]error{
	"repositories_org_id_slug_key": repos.ErrSlugTaken,
}

// transact runs fn in a transaction that acts for actor (transact), mapping a conflict to the domain's error.
func (s *RepoStore) transact(ctx context.Context, what string, actor uuid.UUID, fn func(ctx context.Context, q *queries.Queries) error) error {
	err := transact(ctx, s.pool, s.q, what, actor, fn)
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		if domain, ok := repoConflicts[conflict.Constraint]; ok {
			return domain
		}
	}
	return err
}

// wrapRepo returns repos.ErrNotFound for a query that found no row, and err in what's context otherwise.
func wrapRepo(what string, err error) error {
	if errors.Is(wrap(what, err), auth.ErrNotFound) {
		return repos.ErrNotFound
	}
	return wrap(what, err)
}

// repository is the domain's view of a repositories row, Internal when shared lists it.
func repository(r queries.Repository, shared []uuid.UUID) repos.Repository {
	out := repos.Repository{ID: r.ID, OrgID: r.OrgID, Slug: r.Slug, Name: r.Name, Description: r.Description,
		ObjectFormat: r.ObjectFormat, CreatedAt: r.CreatedAt, Visibility: repos.VisibilityPrivate}
	if r.CreatedBy != nil {
		out.CreatedBy = *r.CreatedBy
	}
	if slices.Contains(shared, r.ID) {
		out.Visibility = repos.VisibilityInternal
	}
	return out
}

// shared lists the IDs of the organization's Internal repositories.
func (s *RepoStore) shared(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := s.q.SharedRepositories(ctx, orgID, auth.RoleOwner)
	if err != nil {
		return nil, fmt.Errorf("listing the shared repositories: %w", err)
	}
	return ids, nil
}

// Create makes the repository, claims its slug, makes its creator its Maintainer, and records it.
func (s *RepoStore) Create(ctx context.Context, actor uuid.UUID, repo repos.Repository) error {
	return s.transact(ctx, "creating the repository", actor, func(ctx context.Context, q *queries.Queries) error {
		if err := q.InsertRepository(ctx, queries.InsertRepositoryParams{
			ID: repo.ID, OrgID: repo.OrgID, Slug: repo.Slug, Name: repo.Name, Description: repo.Description,
			ObjectFormat: repo.ObjectFormat, CreatedBy: &repo.CreatedBy, CreatedAt: repo.CreatedAt,
		}); err != nil {
			return fmt.Errorf("inserting the repository: %w", err)
		}
		if err := q.ClaimRepositorySlug(ctx, repo.OrgID, repo.Slug, repo.ID); err != nil {
			return fmt.Errorf("claiming the slug: %w", err)
		}
		if _, err := q.InsertGrant(ctx, queries.InsertGrantParams{
			PrincipalID: repo.CreatedBy, RoleID: auth.RoleMaintainer, Scope: string(auth.ScopeRepository), ScopeID: &repo.ID, Source: string(auth.SourceGrant),
		}); err != nil {
			return fmt.Errorf("granting its creator Maintainer: %w", err)
		}
		return orgEvent(ctx, q, &repo.OrgID, actor, "repo.created", repo.ID, map[string]any{"slug": repo.Slug, "name": repo.Name})
	})
}

// BySlug finds one of the organization's repositories by any slug it has had there.
func (s *RepoStore) BySlug(ctx context.Context, orgID uuid.UUID, slug string) (repos.Repository, error) {
	row, err := s.q.RepositoryBySlug(ctx, orgID, slug)
	if err != nil {
		return repos.Repository{}, wrapRepo("finding the repository", err)
	}
	shared, err := s.shared(ctx, orgID)
	if err != nil {
		return repos.Repository{}, err
	}
	return repository(row.Repository, shared), nil
}

// ByPath finds a repository by its organization's slug and its own, each one it has or had.
func (s *RepoStore) ByPath(ctx context.Context, orgSlug, slug string) (repos.Repository, error) {
	row, err := s.q.RepositoryByPath(ctx, orgSlug, slug)
	if err != nil {
		return repos.Repository{}, wrapRepo("finding the repository", err)
	}
	shared, err := s.shared(ctx, row.Repository.OrgID)
	if err != nil {
		return repos.Repository{}, err
	}
	return repository(row.Repository, shared), nil
}

// ByID finds a repository.
func (s *RepoStore) ByID(ctx context.Context, id uuid.UUID) (repos.Repository, error) {
	row, err := s.q.RepositoryByID(ctx, id)
	if err != nil {
		return repos.Repository{}, wrapRepo("finding the repository", err)
	}
	shared, err := s.shared(ctx, row.OrgID)
	if err != nil {
		return repos.Repository{}, err
	}
	return repository(row, shared), nil
}

// Among lists those of ids that are the organization's repositories, by slug.
func (s *RepoStore) Among(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]repos.Repository, error) {
	rows, err := s.q.RepositoriesAmong(ctx, orgID, ids)
	if err != nil {
		return nil, fmt.Errorf("listing the repositories: %w", err)
	}
	shared, err := s.shared(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]repos.Repository, 0, len(rows))
	for _, r := range rows {
		out = append(out, repository(r, shared))
	}
	return out, nil
}

// Update writes the repository's settings and claims its slug, keeping the old one as a redirect.
func (s *RepoStore) Update(ctx context.Context, actor uuid.UUID, repo repos.Repository) error {
	return s.transact(ctx, "updating the repository", actor, func(ctx context.Context, q *queries.Queries) error {
		n, err := q.UpdateRepository(ctx, queries.UpdateRepositoryParams{
			ID: repo.ID, Slug: repo.Slug, Name: repo.Name, Description: repo.Description,
		})
		if err != nil {
			return fmt.Errorf("updating the repository: %w", err)
		}
		if n == 0 {
			return repos.ErrNotFound
		}
		if err := q.ClaimRepositorySlug(ctx, repo.OrgID, repo.Slug, repo.ID); err != nil {
			return fmt.Errorf("claiming the slug: %w", err)
		}
		return orgEvent(ctx, q, &repo.OrgID, actor, "repo.updated", repo.ID, map[string]any{"slug": repo.Slug, "name": repo.Name})
	})
}

// Access lists the principals holding grants on the repository with their roles there, by display name.
func (s *RepoStore) Access(ctx context.Context, repoID uuid.UUID) ([]repos.Access, error) {
	rows, err := s.q.RepositoryGrants(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("listing the repository's grants: %w", err)
	}
	var out []repos.Access
	for _, r := range rows {
		if len(out) == 0 || out[len(out)-1].Principal.ID != r.PrincipalID {
			out = append(out, repos.Access{Principal: repos.Principal{ID: r.PrincipalID, Kind: auth.Kind(r.Kind), DisplayName: r.DisplayName}})
		}
		last := &out[len(out)-1]
		last.Roles = append(last.Roles, role(r.Role))
	}
	return out, nil
}

// Roles lists the built-in repository roles, then the organization's own.
func (s *RepoStore) Roles(ctx context.Context, orgID uuid.UUID) ([]orgs.Role, error) {
	rows, err := s.q.RepositoryRoles(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing the roles: %w", err)
	}
	out := make([]orgs.Role, 0, len(rows))
	for _, r := range rows {
		out = append(out, role(r))
	}
	return out, nil
}

// Principal finds one of the organization's members or API keys.
func (s *RepoStore) Principal(ctx context.Context, orgID, principalID uuid.UUID) (repos.Principal, error) {
	row, err := s.q.Grantee(ctx, principalID, orgID)
	if err != nil {
		return repos.Principal{}, wrapRepo("finding the principal", err)
	}
	return repos.Principal{ID: row.ID, Kind: auth.Kind(row.Kind), DisplayName: row.DisplayName}, nil
}

// Grant gives the principal the role on the repository. It holds the organization's lock, as every grant does, so
// the role can't be deleted meanwhile: one deleted first is not found.
func (s *RepoStore) Grant(ctx context.Context, actor uuid.UUID, repo repos.Repository, principalID, roleID uuid.UUID) error {
	return s.transact(ctx, "granting the role", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.LockOrganization(ctx, repo.OrgID); err != nil {
			return wrapRepo("locking the organization", err)
		}
		if _, err := q.Role(ctx, roleID); err != nil {
			return wrapRepo("reading the role", err)
		}
		granted, err := q.InsertGrant(ctx, queries.InsertGrantParams{
			PrincipalID: principalID, RoleID: roleID, Scope: string(auth.ScopeRepository), ScopeID: &repo.ID, Source: string(auth.SourceGrant),
		})
		if err != nil {
			return fmt.Errorf("inserting the grant: %w", err)
		}
		if granted == 0 {
			return repos.ErrGranted
		}
		return orgEvent(ctx, q, &repo.OrgID, actor, "repo.role_granted", principalID, map[string]any{"repo": repo.ID, "role": roleID})
	})
}

// Revoke takes the role from the principal on the repository.
func (s *RepoStore) Revoke(ctx context.Context, actor uuid.UUID, repo repos.Repository, principalID, roleID uuid.UUID) error {
	return s.transact(ctx, "revoking the role", actor, func(ctx context.Context, q *queries.Queries) error {
		n, err := q.DeleteRepositoryGrant(ctx, principalID, roleID, repo.ID)
		if err != nil {
			return fmt.Errorf("deleting the grant: %w", err)
		}
		if n == 0 {
			return repos.ErrNotFound
		}
		return orgEvent(ctx, q, &repo.OrgID, actor, "repo.role_revoked", principalID, map[string]any{"repo": repo.ID, "role": roleID})
	})
}
