package graphql

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// Repositories is what the API needs of the repositories' service.
type Repositories interface {
	Create(ctx context.Context, v repos.Viewer, orgID uuid.UUID, in repos.Input) (repos.Repository, error)
	List(ctx context.Context, v repos.Viewer, orgID uuid.UUID) ([]repos.Repository, error)
	Get(ctx context.Context, v repos.Viewer, orgID uuid.UUID, slug string) (repos.Repository, []string, error)
	ByID(ctx context.Context, v repos.Viewer, repoID uuid.UUID) (repos.Repository, []string, error)
	Update(ctx context.Context, v repos.Viewer, repoID uuid.UUID, in repos.Input) (repos.Repository, error)
	Access(ctx context.Context, v repos.Viewer, repoID uuid.UUID) ([]repos.Access, error)
	Roles(ctx context.Context, v repos.Viewer, repoID uuid.UUID) ([]orgs.Role, error)
	Grant(ctx context.Context, v repos.Viewer, repoID, principalID, roleID uuid.UUID) error
	Revoke(ctx context.Context, v repos.Viewer, repoID, principalID, roleID uuid.UUID) error
}

// repository is r as the API shows it; its lazy fields resolve on their own.
func repository(r repos.Repository) *Repository {
	out := &Repository{ID: r.ID.String(), OrganizationID: r.OrgID.String(), Slug: r.Slug, Name: r.Name, Description: r.Description,
		ObjectFormat: r.ObjectFormat, Visibility: RepositoryVisibility(strings.ToUpper(string(r.Visibility))),
		CreatedAt: r.CreatedAt}
	if r.CreatedBy != uuid.Nil {
		id := r.CreatedBy.String()
		out.CreatedBy = &id
	}
	return out
}

func repositoryAccess(a repos.Access) *RepositoryAccess {
	return &RepositoryAccess{PrincipalID: a.Principal.ID.String(), Kind: string(a.Principal.Kind), DisplayName: a.Principal.DisplayName,
		Roles: mapAll(a.Roles, role)}
}

// repositoryInput is input as the domain takes it.
func repositoryInput(input RepositoryInput) repos.Input {
	in := repos.Input{Slug: input.Slug}
	for field, value := range map[*string]*string{&in.Name: input.Name, &in.Description: input.Description} {
		if value != nil {
			*field = *value
		}
	}
	return in
}

// repoOf is the viewer of a repository's field, and the repository's ID.
func repoOf(ctx context.Context, obj *Repository) (repos.Viewer, uuid.UUID, error) {
	v, err := viewer(ctx)
	if err != nil {
		return repos.Viewer{}, uuid.Nil, err
	}
	repoID, err := parseID(ctx, obj.ID, "repository")
	if err != nil {
		return repos.Viewer{}, uuid.Nil, err
	}
	return v.User, repoID, nil
}

// resolveRepo answers a repository's field through read, for the viewer.
func resolveRepo[T any](ctx context.Context, obj *Repository, read func(context.Context, repos.Viewer, uuid.UUID) (T, error), what string) (T, error) {
	var zero T
	v, repoID, err := repoOf(ctx, obj)
	if err != nil {
		return zero, err
	}
	out, err := read(ctx, v, repoID)
	if err != nil {
		return zero, fail(ctx, err, "repository", what)
	}
	return out, nil
}

// changeRepositoryGrant grants or revokes a role of a principal on a repository, through change, and answers true
// when it did. It reads the repository first, so a repository the viewer may not read is told apart from what missing
// names: the principal of a grant, or the grant a revocation takes.
func (r *mutationResolver) changeRepositoryGrant(ctx context.Context, repositoryID, principalID, roleID string,
	change func(ctx context.Context, v repos.Viewer, repoID, principalID, roleID uuid.UUID) error, missing, what string) (bool, error) {
	v, err := viewer(ctx)
	if err != nil {
		return false, err
	}
	repoID, err := parseID(ctx, repositoryID, "repository")
	if err != nil {
		return false, err
	}
	principal, err := parseID(ctx, principalID, "principal")
	if err != nil {
		return false, err
	}
	role, err := parseID(ctx, roleID, "role")
	if err != nil {
		return false, err
	}
	if _, _, err := r.Repos.ByID(ctx, v.User, repoID); err != nil {
		return false, fail(ctx, err, "repository", what)
	}
	if err := change(ctx, v.User, repoID, principal, role); err != nil {
		return false, fail(ctx, err, missing, what)
	}
	return true, nil
}
