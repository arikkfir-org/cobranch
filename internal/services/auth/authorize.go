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
// has there. scopeID is uuid.Nil at the installation.
type Grants interface {
	Permissions(ctx context.Context, principalID uuid.UUID, scope Scope, scopeID uuid.UUID) ([]string, error)
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

// Holds lists the permissions principalID holds at the scope, sorted.
func (a *Authorizer) Holds(ctx context.Context, principalID uuid.UUID, scope Scope, scopeID uuid.UUID) ([]string, error) {
	permissions, err := a.grants.Permissions(ctx, principalID, scope, scopeID)
	if err != nil {
		return nil, fmt.Errorf("reading %s's permissions: %w", principalID, err)
	}
	slices.Sort(permissions)
	return permissions, nil
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
