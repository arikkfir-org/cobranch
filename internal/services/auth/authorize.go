package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
)

// Scope is where a grant applies: the installation, an organization, a repository, or one secret or one artifact, for
// its own permissions only.
type Scope string

// The scopes.
const (
	ScopeInstallation Scope = "installation"
	ScopeOrganization Scope = "organization"
	ScopeRepository   Scope = "repository"
	ScopeSecret       Scope = "secret"
	ScopeArtifact     Scope = "artifact"
)

// ErrForbidden is a principal that lacks a permission it needs.
var ErrForbidden = errors.New("forbidden")

// Grants is what the authorizer reads: the permissions a principal's roles hold at a scope, through every grant it
// has there. scopeID is uuid.Nil at the installation. A grant reaches its scope and everything inside it: at a
// repository, Permissions answers the permissions of the grants on it and the repository permissions of the roles at
// its organization; at a secret, the secret permissions of the grants on it, on its repository and at its
// organization; at an artifact, the artifact permissions of those grants and of its audiences.
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
// token lists too; under a run's or a delivery's token of theirs, also what the run or the delivery may do there;
// under a task token of theirs, only those the task's assigner holds there too.
func (a *Authorizer) Holds(ctx context.Context, principalID uuid.UUID, scope Scope, scopeID uuid.UUID) ([]string, error) {
	permissions, err := a.grants.Permissions(ctx, principalID, scope, scopeID)
	if err != nil {
		return nil, fmt.Errorf("reading %s's permissions: %w", principalID, err)
	}
	permissions = widened(ctx, principalID, scope, scopeID, narrowed(ctx, principalID, permissions))
	if assigner, capped := assignedBy(ctx, principalID); capped {
		if permissions, err = a.capped(ctx, assigner, permissions, scope, scopeID); err != nil {
			return nil, err
		}
	}
	slices.Sort(permissions)
	return slices.Compact(permissions), nil
}

// runToken is the run's or the delivery's token the request comes with, when it acts as principalID.
func runToken(ctx context.Context, principalID uuid.UUID) (Credential, bool) {
	v, ok := Viewer(ctx)
	if !ok || (v.Credential.Kind != CredentialRunToken && v.Credential.Kind != CredentialDeliveryToken) || v.User.ID != principalID {
		return Credential{}, false
	}
	return v.Credential, true
}

// widened is held and what a run's or a delivery's token of principalID's lets it do at the scope besides: read the
// organization, and do what its job declared, or read, on the repository. A job or a target holds no role but Secret
// reader; the rest it may do only while a run of it runs, or a delivery to it renders, there.
func widened(ctx context.Context, principalID uuid.UUID, scope Scope, scopeID uuid.UUID, held []string) []string {
	run, ok := runToken(ctx, principalID)
	switch {
	case !ok:
		return held
	case scope == ScopeOrganization && scopeID == run.OrgID:
		return append(slices.Clone(held), "org.read")
	case scope == ScopeRepository && scopeID == run.RepoID:
		return append(slices.Clone(held), run.Permissions...)
	}
	return held
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
// a personal token of theirs that doesn't list it, its run's or its delivery's repository under a token of one that
// may, and under a task token of theirs only those where the task's assigner holds it too.
func (a *Authorizer) Repositories(ctx context.Context, principalID, orgID uuid.UUID, permission string) ([]uuid.UUID, error) {
	if len(narrowed(ctx, principalID, []string{permission})) == 0 {
		return nil, nil
	}
	ids, err := a.grants.Repositories(ctx, principalID, orgID, permission)
	if err != nil {
		return nil, fmt.Errorf("listing the repositories where %s holds %s: %w", principalID, permission, err)
	}
	if run, ok := runToken(ctx, principalID); ok && run.OrgID == orgID && slices.Contains(run.Permissions, permission) &&
		!slices.Contains(ids, run.RepoID) {
		ids = append(ids, run.RepoID)
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

// Among lists those of the objects list answers where principalID holds permission, as Holds would answer for each:
// list gives the IDs where a principal's grants give it. None under a personal token of theirs that doesn't list it,
// and under a task token of theirs only those where the task's assigner holds it too. A run's or a delivery's token
// widens nothing here. The services list objects that are scopes of their own, such as artifacts, with it.
func (a *Authorizer) Among(ctx context.Context, principalID uuid.UUID, permission string, list func(ctx context.Context, principalID uuid.UUID) ([]uuid.UUID, error)) ([]uuid.UUID, error) {
	if len(narrowed(ctx, principalID, []string{permission})) == 0 {
		return nil, nil
	}
	ids, err := list(ctx, principalID)
	if err != nil {
		return nil, fmt.Errorf("listing where %s holds %s: %w", principalID, permission, err)
	}
	assigner, capped := assignedBy(ctx, principalID)
	switch {
	case !capped:
		return ids, nil
	case assigner == uuid.Nil:
		return nil, nil
	}
	theirs, err := list(ctx, assigner)
	if err != nil {
		return nil, fmt.Errorf("listing where %s, who assigned the task, holds %s: %w", assigner, permission, err)
	}
	return slices.DeleteFunc(ids, func(id uuid.UUID) bool { return !slices.Contains(theirs, id) }), nil
}

// Renders reports whether the request comes with the token of delivery deliveryID, the one credential that may upload
// its render: nil when it does, ErrForbidden when it doesn't.
func (a *Authorizer) Renders(ctx context.Context, deliveryID uuid.UUID) error {
	v, ok := Viewer(ctx)
	if !ok || v.Credential.Kind != CredentialDeliveryToken || v.Credential.ID != deliveryID {
		return fmt.Errorf("%w: only the delivery's own token uploads its render", ErrForbidden)
	}
	return nil
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
