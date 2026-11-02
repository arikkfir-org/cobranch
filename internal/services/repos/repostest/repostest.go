// Package repostest holds a fake of the repositories domain's port, for the tests of its service and of the adapters
// that serve it: a Store in memory over an orgstest.Store, which keeps the grants the authorizer reads.
package repostest

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/limits"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/repos"
)

// Store is a repos.Store in memory. Its grants and organizations are Orgs', so one authorizer over Orgs reads both.
// Fail, when set, fails every write.
type Store struct {
	Orgs  *orgstest.Store
	Repos map[uuid.UUID]repos.Repository
	// Slugs maps an organization and a slug, old or current, to its repository.
	Slugs  map[[2]string]uuid.UUID
	Events []string
	Fail   error
}

var _ repos.Store = (*Store)(nil)

// NewStore returns an empty Store over orgs.
func NewStore(o *orgstest.Store) *Store {
	return &Store{Orgs: o, Repos: map[uuid.UUID]repos.Repository{}, Slugs: map[[2]string]uuid.UUID{}}
}

func slugKey(orgID uuid.UUID, slug string) [2]string {
	return [2]string{orgID.String(), slug}
}

// shown is repo as the store answers it, Internal once someone besides its creator and the Owners reads it.
func (s *Store) shown(ctx context.Context, repo repos.Repository) repos.Repository {
	repo.Visibility = repos.VisibilityPrivate
	for _, g := range s.Orgs.Grants {
		reaches := (g.Scope == auth.ScopeRepository && g.ScopeID == repo.ID) || (g.Scope == auth.ScopeOrganization && g.ScopeID == repo.OrgID)
		if !reaches || g.Principal == repo.CreatedBy || slices.Contains(s.Orgs.Grants, orgstest.Grant{Principal: g.Principal, Role: auth.RoleOwner, Scope: auth.ScopeOrganization, ScopeID: repo.OrgID}) {
			continue
		}
		held, _ := s.Orgs.Permissions(ctx, g.Principal, auth.ScopeRepository, repo.ID) // the fake's Permissions never fails
		if slices.Contains(held, repos.PermRead) {
			repo.Visibility = repos.VisibilityInternal
		}
	}
	return repo
}

// Create records the repository with its creator as Maintainer, unless its organization holds limit repositories.
func (s *Store) Create(_ context.Context, _ uuid.UUID, repo repos.Repository, limit int64) error {
	if s.Fail != nil {
		return s.Fail
	}
	held := int64(0)
	for _, r := range s.Repos {
		if r.OrgID == repo.OrgID {
			held++
		}
	}
	if limits.Exceeds(limit, held, 1) {
		return &limits.ExceededError{Kind: limits.KindRepositories, Limit: limit, Used: held, Adding: 1}
	}
	for _, r := range s.Repos {
		if r.OrgID == repo.OrgID && r.Slug == repo.Slug {
			return repos.ErrSlugTaken
		}
	}
	s.Repos[repo.ID], s.Slugs[slugKey(repo.OrgID, repo.Slug)] = repo, repo.ID
	s.Orgs.Repos[repo.ID] = repo.OrgID
	s.Orgs.Grant(repo.CreatedBy, auth.RoleMaintainer, auth.ScopeRepository, repo.ID)
	s.Events = append(s.Events, "repo.created")
	return nil
}

// BySlug finds one of the organization's repositories by any of its slugs.
func (s *Store) BySlug(ctx context.Context, orgID uuid.UUID, slug string) (repos.Repository, error) {
	id, ok := s.Slugs[slugKey(orgID, slug)]
	if !ok {
		return repos.Repository{}, repos.ErrNotFound
	}
	return s.shown(ctx, s.Repos[id]), nil
}

// ByPath finds a repository by any slug of its organization's and any of its own.
func (s *Store) ByPath(ctx context.Context, orgSlug, slug string) (repos.Repository, error) {
	orgID, ok := s.Orgs.Slugs[orgSlug]
	if !ok {
		return repos.Repository{}, repos.ErrNotFound
	}
	return s.BySlug(ctx, orgID, slug)
}

// ByID finds a repository.
func (s *Store) ByID(ctx context.Context, id uuid.UUID) (repos.Repository, error) {
	repo, ok := s.Repos[id]
	if !ok {
		return repos.Repository{}, repos.ErrNotFound
	}
	return s.shown(ctx, repo), nil
}

// Among lists those of ids that are the organization's repositories, by slug.
func (s *Store) Among(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]repos.Repository, error) {
	var out []repos.Repository
	for _, id := range ids {
		if r, ok := s.Repos[id]; ok && r.OrgID == orgID {
			out = append(out, s.shown(ctx, r))
		}
	}
	slices.SortFunc(out, func(a, b repos.Repository) int { return strings.Compare(a.Slug, b.Slug) })
	return out, nil
}

// Update writes the repository and claims its slug.
func (s *Store) Update(_ context.Context, _ uuid.UUID, repo repos.Repository) error {
	if s.Fail != nil {
		return s.Fail
	}
	for _, r := range s.Repos {
		if r.ID != repo.ID && r.OrgID == repo.OrgID && r.Slug == repo.Slug {
			return repos.ErrSlugTaken
		}
	}
	s.Repos[repo.ID], s.Slugs[slugKey(repo.OrgID, repo.Slug)] = repo, repo.ID
	s.Events = append(s.Events, "repo.updated")
	return nil
}

// Access lists the principals holding grants on the repository, by display name.
func (s *Store) Access(_ context.Context, repoID uuid.UUID) ([]repos.Access, error) {
	var out []repos.Access
	for _, g := range s.Orgs.Grants {
		if g.Scope != auth.ScopeRepository || g.ScopeID != repoID {
			continue
		}
		role, _ := s.role(g.Role)
		i := slices.IndexFunc(out, func(a repos.Access) bool { return a.Principal.ID == g.Principal })
		if i < 0 {
			out = append(out, repos.Access{Principal: repos.Principal{ID: g.Principal, Kind: auth.KindUser, DisplayName: s.Orgs.Names[g.Principal]}})
			i = len(out) - 1
		}
		out[i].Roles = append(out[i].Roles, role)
	}
	slices.SortFunc(out, func(a, b repos.Access) int { return strings.Compare(a.Principal.DisplayName, b.Principal.DisplayName) })
	return out, nil
}

func (s *Store) role(id uuid.UUID) (orgs.Role, bool) {
	for _, r := range slices.Concat(s.Orgs.RepositoryRoles, s.Orgs.RoleList) {
		if r.ID == id {
			return r, true
		}
	}
	return orgs.Role{}, false
}

// Roles lists the built-in repository roles, then the organization's own.
func (s *Store) Roles(_ context.Context, orgID uuid.UUID) ([]orgs.Role, error) {
	out := slices.Clone(s.Orgs.RepositoryRoles)
	for _, r := range s.Orgs.RoleList {
		if !r.BuiltIn && (r.OrgID == orgID || r.OrgID == uuid.Nil) {
			out = append(out, r)
		}
	}
	return out, nil
}

// Principal finds one of the organization's members or API keys.
func (s *Store) Principal(_ context.Context, orgID, principalID uuid.UUID) (repos.Principal, error) {
	if slices.Contains(s.Orgs.Memberships[orgID], principalID) {
		return repos.Principal{ID: principalID, Kind: auth.KindUser, DisplayName: s.Orgs.Names[principalID]}, nil
	}
	if k, ok := s.Orgs.Keys[principalID]; ok && k.OrgID == orgID {
		return repos.Principal{ID: principalID, Kind: auth.KindAPIKey, DisplayName: k.Name}, nil
	}
	return repos.Principal{}, repos.ErrNotFound
}

// Grant gives the principal the role on the repository.
func (s *Store) Grant(_ context.Context, _ uuid.UUID, repo repos.Repository, principalID, roleID uuid.UUID) error {
	if s.Fail != nil {
		return s.Fail
	}
	g := orgstest.Grant{Principal: principalID, Role: roleID, Scope: auth.ScopeRepository, ScopeID: repo.ID}
	if slices.Contains(s.Orgs.Grants, g) {
		return repos.ErrGranted
	}
	s.Orgs.Grants = append(s.Orgs.Grants, g)
	s.Events = append(s.Events, "repo.role_granted")
	return nil
}

// Revoke takes the role from the principal on the repository.
func (s *Store) Revoke(_ context.Context, _ uuid.UUID, repo repos.Repository, principalID, roleID uuid.UUID) error {
	if s.Fail != nil {
		return s.Fail
	}
	g := orgstest.Grant{Principal: principalID, Role: roleID, Scope: auth.ScopeRepository, ScopeID: repo.ID}
	i := slices.Index(s.Orgs.Grants, g)
	if i < 0 {
		return repos.ErrNotFound
	}
	s.Orgs.Grants = slices.Delete(s.Orgs.Grants, i, i+1)
	s.Events = append(s.Events, "repo.role_revoked")
	return nil
}
