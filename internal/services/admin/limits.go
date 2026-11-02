package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/limits"
	"cobranch.dev/internal/services/orgs"
)

// OwnLimits are an organization's own limits as the database keeps them, which win over the installation's, and when
// and by whom they last changed.
type OwnLimits struct {
	limits.Overrides
	// Version counts the changes; 0 while nobody has set any.
	Version   int64
	UpdatedAt time.Time
	// UpdatedBy is who changed them last: uuid.Nil when nobody has, or they're gone.
	UpdatedBy uuid.UUID
}

// OrganizationLimits are an organization's limits as the Back-office shows them: its own, and the installation's, which
// apply where it has none.
type OrganizationLimits struct {
	ID   uuid.UUID
	Slug string
	Name string
	Own  OwnLimits
	// Installation are the installation's limits: the values', else an administrator's, else the built-in defaults.
	Installation limits.Limits
}

// Applies returns the limits that apply to the organization.
func (o OrganizationLimits) Applies() limits.Limits {
	return o.Installation.For(o.Own.Overrides)
}

// Fingerprint changes whenever what the limits show does: the organization's own, or the installation's. The Admin
// API's ETag is made of it.
func (o OrganizationLimits) Fingerprint() string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%d|%s|%s|%d|%d", o.ID, o.Own.Version, optional(o.Own.Repositories),
		optional(o.Own.GitData), o.Installation.RepositoriesPerOrganization, o.Installation.GitDataPerOrganization))
	return hex.EncodeToString(sum[:12])
}

// optional writes n, or - when there is none.
func optional(n *int64) string {
	if n == nil {
		return "-"
	}
	return strconv.FormatInt(*n, 10)
}

// LimitChange changes one of an organization's own limits when Set: to Value, or back to the installation's when Value
// is nil.
type LimitChange struct {
	Set   bool
	Value *int64
}

// OwnLimitsChange changes an organization's own limits: one not Set stays as it is.
type OwnLimitsChange struct {
	Repositories LimitChange
	GitData      LimitChange
}

// OrganizationLimits reads the limits of the organization slug names; it takes installation.manage.
func (s *Service) OrganizationLimits(ctx context.Context, v auth.User, slug string) (OrganizationLimits, error) {
	if err := s.require(ctx, v, PermManage); err != nil {
		return OrganizationLimits{}, err
	}
	org, err := s.bySlug(ctx, slug)
	if err != nil {
		return OrganizationLimits{}, err
	}
	return s.organizationLimits(ctx, org)
}

// ChangeOrganizationLimits makes change to the own limits of the organization slug names, unless they changed since
// the caller read them, as their fingerprint match says (* for any): then ErrStale. The organization's audit trail
// records the change. It takes installation.manage.
func (s *Service) ChangeOrganizationLimits(ctx context.Context, v auth.User, slug, match string, change OwnLimitsChange) (OrganizationLimits, error) {
	if err := s.require(ctx, v, PermManage); err != nil {
		return OrganizationLimits{}, err
	}
	org, err := s.bySlug(ctx, slug)
	if err != nil {
		return OrganizationLimits{}, err
	}
	current, err := s.organizationLimits(ctx, org)
	if err != nil {
		return OrganizationLimits{}, err
	}
	if match != "*" && current.Fingerprint() != match {
		return OrganizationLimits{}, ErrStale
	}
	next := current.Own
	for _, l := range []struct {
		field  string
		change LimitChange
		to     **int64
	}{{"repositories", change.Repositories, &next.Repositories}, {"gitData", change.GitData, &next.GitData}} {
		if !l.change.Set {
			continue
		}
		if l.change.Value != nil && *l.change.Value < 0 {
			return OrganizationLimits{}, &orgs.ValidationError{Field: l.field, Code: "limit.invalid", Message: "a limit is 0, for none, or more"}
		}
		*l.to = l.change.Value
	}
	written, err := s.store.ChangeOwnLimits(ctx, v.ID, org.ID, next, s.now())
	if errors.Is(err, ErrStale) {
		return OrganizationLimits{}, ErrStale
	}
	if err != nil {
		return OrganizationLimits{}, fmt.Errorf("changing the organization's limits: %w", err)
	}
	current.Own = written
	return current, nil
}

// organizationLimits reads org's own limits and the installation's.
func (s *Service) organizationLimits(ctx context.Context, org orgs.Organization) (OrganizationLimits, error) {
	installation, err := s.installationLimits(ctx)
	if err != nil {
		return OrganizationLimits{}, err
	}
	own, err := s.store.OwnLimits(ctx, org.ID)
	if err != nil {
		return OrganizationLimits{}, fmt.Errorf("reading the organization's limits: %w", err)
	}
	return OrganizationLimits{ID: org.ID, Slug: org.Slug, Name: org.Name, Own: own, Installation: installation}, nil
}

// installationLimits are the installation's limits: each the values', else the database's, else the built-in default.
func (s *Service) installationLimits(ctx context.Context) (limits.Limits, error) {
	stored, err := s.store.InstallationSettings(ctx)
	if err != nil {
		return limits.Limits{}, fmt.Errorf("reading the installation's limits: %w", err)
	}
	return limits.Resolve(s.limitValues, stored.Limits), nil
}
