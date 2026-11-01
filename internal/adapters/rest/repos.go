package rest

import (
	"context"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// Repositories is what the API needs of the repositories' service.
type Repositories interface {
	Create(ctx context.Context, v repos.Viewer, orgID uuid.UUID, in repos.Input) (repos.Repository, error)
	List(ctx context.Context, v repos.Viewer, orgID uuid.UUID) ([]repos.Repository, error)
	Get(ctx context.Context, v repos.Viewer, orgID uuid.UUID, slug string) (repos.Repository, []string, error)
	Locate(ctx context.Context, v repos.Viewer, orgSlug, slug string) (repos.Repository, []string, error)
	ByID(ctx context.Context, v repos.Viewer, repoID uuid.UUID) (repos.Repository, []string, error)
	Update(ctx context.Context, v repos.Viewer, repoID uuid.UUID, in repos.Input) (repos.Repository, error)
	Access(ctx context.Context, v repos.Viewer, repoID uuid.UUID) ([]repos.Access, error)
	Roles(ctx context.Context, v repos.Viewer, repoID uuid.UUID) ([]orgs.Role, error)
	Grant(ctx context.Context, v repos.Viewer, repoID, principalID, roleID uuid.UUID) error
	Revoke(ctx context.Context, v repos.Viewer, repoID, principalID, roleID uuid.UUID) error
}

// repository is r as the API shows it, with what the viewer holds there.
func repository(r repos.Repository, held []string) Repository {
	out := Repository{Id: r.ID, OrgId: r.OrgID, Slug: r.Slug, Name: r.Name, Description: r.Description,
		ObjectFormat: RepositoryObjectFormat(r.ObjectFormat), Visibility: RepositoryVisibility(r.Visibility), CreatedAt: r.CreatedAt,
		ViewerPermissions: nonNil(held)}
	if r.CreatedBy != uuid.Nil {
		out.CreatedBy = &r.CreatedBy
	}
	return out
}

func access(a repos.Access) RepositoryAccess {
	return RepositoryAccess{PrincipalId: a.Principal.ID, Kind: RepositoryAccessKind(a.Principal.Kind), DisplayName: a.Principal.DisplayName,
		Roles: mapAll(a.Roles, role)}
}

// repo finds the repository the path names, by its organization's slug and its own, current or old, for a viewer
// with repo.read on it.
func (a api) repo(ctx context.Context, v repos.Viewer, orgSlug, slug string) (repos.Repository, []string, error) {
	org, err := a.org(ctx, v, orgSlug)
	if err != nil {
		return repos.Repository{}, nil, err
	}
	repo, held, err := a.repos.Get(ctx, v, org.ID, slug)
	if err != nil {
		return repos.Repository{}, nil, fail(err, "repository")
	}
	return repo, held, nil
}

// ListRepositories lists the organization's repositories the viewer reads.
func (a api) ListRepositories(ctx context.Context, req ListRepositoriesRequestObject) (ListRepositoriesResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	list, err := a.repos.List(ctx, v, org.ID)
	if err != nil {
		return nil, fail(err, "organization")
	}
	out := make([]Repository, 0, len(list))
	for _, r := range list {
		_, held, err := a.repos.ByID(ctx, v, r.ID)
		if err != nil {
			return nil, fail(err, "repository")
		}
		out = append(out, repository(r, held))
	}
	return ListRepositories200JSONResponse{Repositories: out}, nil
}

// CreateRepository makes a repository with the viewer its Maintainer.
func (a api) CreateRepository(ctx context.Context, req CreateRepositoryRequestObject) (CreateRepositoryResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	b := req.Body
	in := repos.Input{Slug: b.Slug}
	for field, value := range map[*string]*string{&in.Name: b.Name, &in.Description: b.Description} {
		if value != nil {
			*field = *value
		}
	}
	repo, err := a.repos.Create(ctx, v, org.ID, in)
	if err != nil {
		return nil, fail(err, "organization")
	}
	_, held, err := a.repos.ByID(ctx, v, repo.ID)
	if err != nil {
		return nil, fail(err, "repository")
	}
	return CreateRepository201JSONResponse(repository(repo, held)), nil
}

// GetRepository answers the repository the slugs name.
func (a api) GetRepository(ctx context.Context, req GetRepositoryRequestObject) (GetRepositoryResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, held, err := a.repo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	return GetRepository200JSONResponse(repository(repo, held)), nil
}

// UpdateRepository changes what the body names and keeps the rest.
func (a api) UpdateRepository(ctx context.Context, req UpdateRepositoryRequestObject) (UpdateRepositoryResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, _, err := a.repo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	in := repos.Input{Slug: repo.Slug, Name: repo.Name, Description: repo.Description}
	b := req.Body
	for field, value := range map[*string]*string{&in.Slug: b.Slug, &in.Name: b.Name, &in.Description: b.Description} {
		if value != nil {
			*field = *value
		}
	}
	updated, err := a.repos.Update(ctx, v, repo.ID, in)
	if err != nil {
		return nil, fail(err, "repository")
	}
	_, held, err := a.repos.ByID(ctx, v, repo.ID)
	if err != nil {
		return nil, fail(err, "repository")
	}
	return UpdateRepository200JSONResponse(repository(updated, held)), nil
}

// ListRepositoryRoles lists the roles the repository grants.
func (a api) ListRepositoryRoles(ctx context.Context, req ListRepositoryRolesRequestObject) (ListRepositoryRolesResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, _, err := a.repo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	roles, err := a.repos.Roles(ctx, v, repo.ID)
	if err != nil {
		return nil, fail(err, "repository")
	}
	return ListRepositoryRoles200JSONResponse{Roles: mapAll(roles, role)}, nil
}

// ListRepositoryAccess lists who holds grants on the repository.
func (a api) ListRepositoryAccess(ctx context.Context, req ListRepositoryAccessRequestObject) (ListRepositoryAccessResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, _, err := a.repo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	list, err := a.repos.Access(ctx, v, repo.ID)
	if err != nil {
		return nil, fail(err, "repository")
	}
	return ListRepositoryAccess200JSONResponse{Access: mapAll(list, access)}, nil
}

// GrantRepositoryRole gives a principal a role on the repository.
func (a api) GrantRepositoryRole(ctx context.Context, req GrantRepositoryRoleRequestObject) (GrantRepositoryRoleResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, _, err := a.repo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	if err := a.repos.Grant(ctx, v, repo.ID, req.PrincipalID, req.Body.RoleId); err != nil {
		return nil, fail(err, "principal")
	}
	return GrantRepositoryRole204Response{}, nil
}

// RevokeRepositoryRole takes a role from a principal on the repository.
func (a api) RevokeRepositoryRole(ctx context.Context, req RevokeRepositoryRoleRequestObject) (RevokeRepositoryRoleResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, _, err := a.repo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	if err := a.repos.Revoke(ctx, v, repo.ID, req.PrincipalID, req.RoleID); err != nil {
		return nil, fail(err, "grant")
	}
	return RevokeRepositoryRole204Response{}, nil
}
