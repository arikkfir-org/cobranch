package repos

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// Store is what the database keeps for repositories. Each method is one transaction, which acts for actor where one
// is named, so the audit log records who changed each row.
type Store interface {
	// Create makes the repository, claims its slug, grants its creator Maintainer on it and records the event, or
	// ErrSlugTaken.
	Create(ctx context.Context, actor uuid.UUID, repo Repository) error
	// BySlug finds one of the organization's repositories by any slug it has had there, or ErrNotFound.
	BySlug(ctx context.Context, orgID uuid.UUID, slug string) (Repository, error)
	// ByID finds a repository, or ErrNotFound.
	ByID(ctx context.Context, id uuid.UUID) (Repository, error)
	// Among lists those of ids that are the organization's repositories, by slug.
	Among(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]Repository, error)
	// Update writes the repository's settings, claiming its slug when it changed, or ErrSlugTaken.
	Update(ctx context.Context, actor uuid.UUID, repo Repository) error
	// Access lists the principals holding grants on the repository with their roles there, by display name.
	Access(ctx context.Context, repoID uuid.UUID) ([]Access, error)
	// Roles lists the roles the organization's repositories grant: the built-in repository roles, then its own.
	Roles(ctx context.Context, orgID uuid.UUID) ([]orgs.Role, error)
	// Principal finds one of the organization's members or API keys, or ErrNotFound.
	Principal(ctx context.Context, orgID, principalID uuid.UUID) (Principal, error)
	// Grant gives the principal the role on the repository and records it, or ErrGranted when it holds it.
	Grant(ctx context.Context, actor uuid.UUID, repo Repository, principalID, roleID uuid.UUID) error
	// Revoke takes the role from the principal on the repository and records it, or ErrNotFound when it doesn't hold
	// it.
	Revoke(ctx context.Context, actor uuid.UUID, repo Repository, principalID, roleID uuid.UUID) error
}

// The permissions this slice checks.
const (
	// PermCreate creates repositories in an organization.
	PermCreate = "repos.create"
	// PermRead reads a repository; without it, the repository answers like one that doesn't exist.
	PermRead = "repo.read"
	// PermManage changes a repository's settings.
	PermManage = "repo.manage"
)

// Service runs repositories: every operation checks its viewer's permissions through the authorizer first.
type Service struct {
	store Store
	authz *auth.Authorizer
	now   func() time.Time
}

// NewService returns a Service over store, checking permissions with authz.
func NewService(store Store, authz *auth.Authorizer) *Service {
	return &Service{store: store, authz: authz, now: time.Now}
}

// SetClock makes the service read the time from now: for tests.
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

// Viewer is who an operation runs for: a signed-in user, or an API key.
type Viewer = auth.User

// orgHolds returns what v holds at the organization, or ErrNotFound without org.read, so nobody learns that an
// organization they can't see exists.
func (s *Service) orgHolds(ctx context.Context, v Viewer, orgID uuid.UUID) ([]string, error) {
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeOrganization, orgID)
	if err != nil {
		return nil, fmt.Errorf("reading the viewer's permissions: %w", err)
	}
	if !slices.Contains(held, orgs.PermRead) {
		return nil, ErrNotFound
	}
	return held, nil
}

// read returns what v holds at repo, or ErrNotFound without repo.read.
func (s *Service) read(ctx context.Context, v Viewer, repo Repository) ([]string, error) {
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeRepository, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("reading the viewer's permissions: %w", err)
	}
	if !slices.Contains(held, PermRead) {
		return nil, ErrNotFound
	}
	return held, nil
}

// require returns the repository and what v holds there: ErrNotFound without repo.read, auth.ErrForbidden without
// permission.
func (s *Service) require(ctx context.Context, v Viewer, repoID uuid.UUID, permission string) (Repository, []string, error) {
	repo, err := s.store.ByID(ctx, repoID)
	if err != nil {
		return Repository{}, nil, wrap("finding the repository", err)
	}
	held, err := s.read(ctx, v, repo)
	if err != nil {
		return Repository{}, nil, err
	}
	if !slices.Contains(held, permission) {
		return Repository{}, nil, fmt.Errorf("%w: %s on the repository", auth.ErrForbidden, permission)
	}
	return repo, held, nil
}

// Create makes an empty repository in the organization, its creator v its Maintainer; it takes repos.create there.
func (s *Service) Create(ctx context.Context, v Viewer, orgID uuid.UUID, in Input) (Repository, error) {
	held, err := s.orgHolds(ctx, v, orgID)
	if err != nil {
		return Repository{}, err
	}
	if !slices.Contains(held, PermCreate) {
		return Repository{}, fmt.Errorf("%w: %s at the organization", auth.ErrForbidden, PermCreate)
	}
	in, err = Validate(in)
	if err != nil {
		return Repository{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Repository{}, fmt.Errorf("making an ID: %w", err)
	}
	repo := Repository{ID: id, OrgID: orgID, Slug: in.Slug, Name: in.Name, Description: in.Description, DefaultBranch: in.DefaultBranch,
		ObjectFormat: ObjectFormatSHA1, CreatedBy: v.ID, CreatedAt: s.now()}
	if err := s.store.Create(ctx, v.ID, repo); err != nil {
		return Repository{}, wrap("creating the repository", err)
	}
	// Read back for its visibility, which the store infers: an organization role may read it from the start.
	created, err := s.store.ByID(ctx, repo.ID)
	if err != nil {
		return Repository{}, wrap("reading the repository back", err)
	}
	return created, nil
}

// List lists the organization's repositories v reads, by slug; it takes org.read.
func (s *Service) List(ctx context.Context, v Viewer, orgID uuid.UUID) ([]Repository, error) {
	if _, err := s.orgHolds(ctx, v, orgID); err != nil {
		return nil, err
	}
	ids, err := s.authz.Repositories(ctx, v.ID, orgID, PermRead)
	if err != nil {
		return nil, fmt.Errorf("listing what the viewer reads: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	repos, err := s.store.Among(ctx, orgID, ids)
	if err != nil {
		return nil, fmt.Errorf("listing the repositories: %w", err)
	}
	return repos, nil
}

// Get finds the organization's repository slug names, by its current slug or an old one, with what v holds there,
// for a viewer with repo.read on it; anyone else gets ErrNotFound.
func (s *Service) Get(ctx context.Context, v Viewer, orgID uuid.UUID, slug string) (Repository, []string, error) {
	if _, err := s.orgHolds(ctx, v, orgID); err != nil {
		return Repository{}, nil, err
	}
	repo, err := s.store.BySlug(ctx, orgID, slug)
	if err != nil {
		return Repository{}, nil, wrap("finding the repository", err)
	}
	held, err := s.read(ctx, v, repo)
	if err != nil {
		return Repository{}, nil, err
	}
	return repo, held, nil
}

// ByID finds a repository with what v holds there, for a viewer with repo.read on it; anyone else gets ErrNotFound.
func (s *Service) ByID(ctx context.Context, v Viewer, repoID uuid.UUID) (Repository, []string, error) {
	return s.require(ctx, v, repoID, PermRead)
}

// Update changes the repository's slug, name, description or default branch; it takes repo.manage. A new slug keeps
// the old one as a redirect.
func (s *Service) Update(ctx context.Context, v Viewer, repoID uuid.UUID, in Input) (Repository, error) {
	repo, _, err := s.require(ctx, v, repoID, PermManage)
	if err != nil {
		return Repository{}, err
	}
	in, err = Validate(in)
	if err != nil {
		return Repository{}, err
	}
	repo.Slug, repo.Name, repo.Description, repo.DefaultBranch = in.Slug, in.Name, in.Description, in.DefaultBranch
	if err := s.store.Update(ctx, v.ID, repo); err != nil {
		return Repository{}, wrap("updating the repository", err)
	}
	return repo, nil
}

// Access lists who holds grants on the repository, with their roles there; it takes repo.read. The organization's
// roles reach it too, as its members page shows.
func (s *Service) Access(ctx context.Context, v Viewer, repoID uuid.UUID) ([]Access, error) {
	if _, _, err := s.require(ctx, v, repoID, PermRead); err != nil {
		return nil, err
	}
	access, err := s.store.Access(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("listing the repository's grants: %w", err)
	}
	for i := range access {
		for j, r := range access[i].Roles {
			access[i].Roles[j].Permissions = RepositoryPermissions(r.Permissions)
		}
	}
	return access, nil
}

// Roles lists the roles the repository grants, each with the permissions it holds there: the built-in repository
// roles, then the organization's own that hold any; it takes repo.read.
func (s *Service) Roles(ctx context.Context, v Viewer, repoID uuid.UUID) ([]orgs.Role, error) {
	repo, _, err := s.require(ctx, v, repoID, PermRead)
	if err != nil {
		return nil, err
	}
	return s.roles(ctx, repo.OrgID, false)
}

// roles lists the roles the organization's repositories grant, kept to their repository permissions. With all, it
// keeps the organization's roles that hold none too: a role may have lost them since it was granted on a repository.
func (s *Service) roles(ctx context.Context, orgID uuid.UUID, all bool) ([]orgs.Role, error) {
	list, err := s.store.Roles(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing the roles: %w", err)
	}
	var out []orgs.Role
	for _, r := range list {
		if r.Permissions = RepositoryPermissions(r.Permissions); all || len(r.Permissions) > 0 {
			out = append(out, r)
		}
	}
	return out, nil
}

// grantable finds the repository, the role and the principal a grant or a revocation names, for a viewer with
// org.grants.manage at the repository's organization who holds every permission the role gives on the repository. A
// revocation finds the role among all the organization's, so a grant whose role has since lost its repository
// permissions can still be taken back.
func (s *Service) grantable(ctx context.Context, v Viewer, repoID, principalID, roleID uuid.UUID, revoking bool) (Repository, orgs.Role, error) {
	repo, held, err := s.require(ctx, v, repoID, PermRead)
	if err != nil {
		return Repository{}, orgs.Role{}, err
	}
	atOrg, err := s.orgHolds(ctx, v, repo.OrgID)
	if err != nil {
		return Repository{}, orgs.Role{}, err
	}
	if !slices.Contains(atOrg, orgs.PermGrantsManage) {
		return Repository{}, orgs.Role{}, fmt.Errorf("%w: %s at the organization", auth.ErrForbidden, orgs.PermGrantsManage)
	}
	roles, err := s.roles(ctx, repo.OrgID, revoking)
	if err != nil {
		return Repository{}, orgs.Role{}, err
	}
	i := slices.IndexFunc(roles, func(r orgs.Role) bool { return r.ID == roleID })
	if i < 0 {
		return Repository{}, orgs.Role{}, &orgs.ValidationError{Field: "roleId", Code: "role.invalid", Message: "not a role this repository grants"}
	}
	if !auth.Covers(held, roles[i].Permissions) {
		return Repository{}, orgs.Role{}, fmt.Errorf("%w: granting %s takes every permission it gives on the repository", auth.ErrForbidden, roles[i].Name)
	}
	if _, err := s.store.Principal(ctx, repo.OrgID, principalID); err != nil {
		return Repository{}, orgs.Role{}, wrap("finding the principal", err)
	}
	return repo, roles[i], nil
}

// Grant gives one of the organization's members or API keys a role on the repository; it takes org.grants.manage
// and every permission the role gives there.
func (s *Service) Grant(ctx context.Context, v Viewer, repoID, principalID, roleID uuid.UUID) error {
	repo, role, err := s.grantable(ctx, v, repoID, principalID, roleID, false)
	if err != nil {
		return err
	}
	return wrap("granting the role", s.store.Grant(ctx, v.ID, repo, principalID, role.ID))
}

// Revoke takes a role from a principal on the repository; it takes org.grants.manage and every permission the role
// gives there.
func (s *Service) Revoke(ctx context.Context, v Viewer, repoID, principalID, roleID uuid.UUID) error {
	repo, role, err := s.grantable(ctx, v, repoID, principalID, roleID, true)
	if err != nil {
		return err
	}
	return wrap("revoking the role", s.store.Revoke(ctx, v.ID, repo, principalID, role.ID))
}

// wrap keeps the domain's own errors as they are and gives any other what's context.
func wrap(what string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrSlugTaken), errors.Is(err, ErrGranted):
		return err
	}
	return fmt.Errorf("%s: %w", what, err)
}
