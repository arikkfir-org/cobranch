package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
)

// Scope is where a grant applies: the installation, an organization or a repository.
type Scope string

// The scopes.
const (
	ScopeInstallation Scope = "installation"
	ScopeOrganization Scope = "organization"
	ScopeRepository   Scope = "repository"
)

// ErrForbidden is a principal that lacks a permission it needs.
var ErrForbidden = errors.New("forbidden")

// Grants is what the authorizer reads: the permissions a principal's roles hold at a scope, through every grant it
// has there. scopeID is uuid.Nil at the installation. A grant reaches its scope and everything inside it: at a
// repository, Permissions answers the permissions of the grants on it and the repository permissions of the roles at
// its organization.
type Grants interface {
	Permissions(ctx context.Context, principalID uuid.UUID, scope Scope, scopeID uuid.UUID) ([]string, error)
	// Repositories lists the IDs of the organization's repositories where the principal holds permission, through a
	// grant on the repository or a role at the organization.
	Repositories(ctx context.Context, principalID, orgID uuid.UUID, permission string) ([]uuid.UUID, error)
}

// Authorizer is the service layer's one authorizer: every check of a permission goes through it, and nothing is
// cached between requests, so a revoked grant fails the very next one
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/principals-and-permissions.md).
type Authorizer struct {
	grants Grants
}

// NewAuthorizer returns an Authorizer that reads grants.
func NewAuthorizer(grants Grants) *Authorizer {
	return &Authorizer{grants: grants}
}

// Holds lists the permissions principalID holds at the scope, sorted: under a personal token of theirs, only those the
// token lists too.
func (a *Authorizer) Holds(ctx context.Context, principalID uuid.UUID, scope Scope, scopeID uuid.UUID) ([]string, error) {
	permissions, err := a.grants.Permissions(ctx, principalID, scope, scopeID)
	if err != nil {
		return nil, fmt.Errorf("reading %s's permissions: %w", principalID, err)
	}
	permissions = narrowed(ctx, principalID, permissions)
	slices.Sort(permissions)
	return permissions, nil
}

// narrowed is what of held principalID may use under the request's credential: all of it, unless the request comes
// with a personal token of principalID's, which uses only the permissions it lists.
func narrowed(ctx context.Context, principalID uuid.UUID, held []string) []string {
	v, ok := Viewer(ctx)
	if !ok || v.Credential.Kind != CredentialPersonalToken || v.User.ID != principalID {
		return held
	}
	return slices.DeleteFunc(slices.Clone(held), func(p string) bool { return !slices.Contains(v.Credential.Permissions, p) })
}

// Can reports whether principalID holds permission at the scope: nil when it does, ErrForbidden when it doesn't.
func (a *Authorizer) Can(ctx context.Context, principalID uuid.UUID, permission string, scope Scope, scopeID uuid.UUID) error {
	held, err := a.Holds(ctx, principalID, scope, scopeID)
	if err != nil {
		return err
	}
	if !slices.Contains(held, permission) {
		return fmt.Errorf("%w: %s at the %s", ErrForbidden, permission, scope)
	}
	return nil
}

// Repositories lists the IDs of the organization's repositories where principalID holds permission: every one of
// them when one of its roles at the organization holds it, and otherwise those its grants on them give it; none under
// a personal token of theirs that doesn't list it.
func (a *Authorizer) Repositories(ctx context.Context, principalID, orgID uuid.UUID, permission string) ([]uuid.UUID, error) {
	if len(narrowed(ctx, principalID, []string{permission})) == 0 {
		return nil, nil
	}
	ids, err := a.grants.Repositories(ctx, principalID, orgID, permission)
	if err != nil {
		return nil, fmt.Errorf("listing the repositories where %s holds %s: %w", principalID, permission, err)
	}
	return ids, nil
}

// Covers reports whether held includes every one of permissions: the grant rules, under which nobody grants or
// revokes more than they hold.
func Covers(held, permissions []string) bool {
	for _, p := range permissions {
		if !slices.Contains(held, p) {
			return false
		}
	}
	return true
}
