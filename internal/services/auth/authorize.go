package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
)

// Scope is where a grant applies: the installation, an organization, a repository, or one secret, for secret
// permissions only.
type Scope string

// The scopes.
const (
	ScopeInstallation Scope = "installation"
	ScopeOrganization Scope = "organization"
	ScopeRepository   Scope = "repository"
	ScopeSecret       Scope = "secret"
)

// ErrForbidden is a principal that lacks a permission it needs.
var ErrForbidden = errors.New("forbidden")

// Grants is what the authorizer reads: the permissions a principal's roles hold at a scope, through every grant it
// has there. scopeID is uuid.Nil at the installation. A grant reaches its scope and everything inside it: at a
// repository, Permissions answers the permissions of the grants on it and the repository permissions of the roles at
// its organization; at a secret, the secret permissions of the grants on it, on its repository and at its
// organization.
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
// token lists too; under a task token of theirs, only those the task's assigner holds there too.
func (a *Authorizer) Holds(ctx context.Context, principalID uuid.UUID, scope Scope, scopeID uuid.UUID) ([]string, error) {
	permissions, err := a.grants.Permissions(ctx, principalID, scope, scopeID)
	if err != nil {
		return nil, fmt.Errorf("reading %s's permissions: %w", principalID, err)
	}
	permissions = narrowed(ctx, principalID, permissions)
	if assigner, capped := assignedBy(ctx, principalID); capped {
		if permissions, err = a.capped(ctx, assigner, permissions, scope, scopeID); err != nil {
			return nil, err
		}
	}
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

// assignedBy reports whether the request comes with a task token of principalID's whose assigner caps what
// principalID holds, and who that assigner is: uuid.Nil once the assigner is gone, when the token holds nothing. A task
// Cobranch started names the agent itself, whose own grants are the cap.
func assignedBy(ctx context.Context, principalID uuid.UUID) (uuid.UUID, bool) {
	v, ok := Viewer(ctx)
	if !ok || v.Credential.Kind != CredentialTaskToken || v.User.ID != principalID || v.Credential.Task.AssignerID == principalID {
		return uuid.Nil, false
	}
	return v.Credential.Task.AssignerID, true
}

// capped keeps of held what the assigner holds at the scope too: nothing for uuid.Nil.
func (a *Authorizer) capped(ctx context.Context, assigner uuid.UUID, held []string, scope Scope, scopeID uuid.UUID) ([]string, error) {
	if assigner == uuid.Nil {
		return nil, nil
	}
	theirs, err := a.grants.Permissions(ctx, assigner, scope, scopeID)
	if err != nil {
		return nil, fmt.Errorf("reading the permissions of %s, who assigned the task: %w", assigner, err)
	}
	return slices.DeleteFunc(slices.Clone(held), func(p string) bool { return !slices.Contains(theirs, p) }), nil
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
// a personal token of theirs that doesn't list it, and under a task token of theirs only those where the task's
// assigner holds it too.
func (a *Authorizer) Repositories(ctx context.Context, principalID, orgID uuid.UUID, permission string) ([]uuid.UUID, error) {
	if len(narrowed(ctx, principalID, []string{permission})) == 0 {
		return nil, nil
	}
	ids, err := a.grants.Repositories(ctx, principalID, orgID, permission)
	if err != nil {
		return nil, fmt.Errorf("listing the repositories where %s holds %s: %w", principalID, permission, err)
	}
	assigner, capped := assignedBy(ctx, principalID)
	switch {
	case !capped:
		return ids, nil
	case assigner == uuid.Nil:
		return nil, nil
	}
	theirs, err := a.grants.Repositories(ctx, assigner, orgID, permission)
	if err != nil {
		return nil, fmt.Errorf("listing the repositories where %s, who assigned the task, holds %s: %w", assigner, permission, err)
	}
	kept := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if slices.Contains(theirs, id) {
			kept = append(kept, id)
		}
	}
	return kept, nil
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
