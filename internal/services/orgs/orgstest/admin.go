package orgstest

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

var _ admin.Store = (*Store)(nil)

// newestFirst sorts ids by their UUIDv7 order, newest first, and keeps those after `after` (uuid.Nil: all).
func newestFirst(ids []uuid.UUID, after uuid.UUID) []uuid.UUID {
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(b.String(), a.String()) })
	if after == uuid.Nil {
		return ids
	}
	return slices.DeleteFunc(ids, func(id uuid.UUID) bool { return id.String() >= after.String() })
}

// holds reports whether any of values holds query, in any case.
func holds(query string, values ...string) bool {
	return slices.ContainsFunc(values, func(v string) bool { return strings.Contains(strings.ToLower(v), strings.ToLower(query)) })
}

// AllOrganizations lists a page of the organizations.
func (s *Store) AllOrganizations(_ context.Context, page admin.Page) ([]admin.Organization, error) {
	var ids []uuid.UUID
	for id := range s.Orgs {
		ids = append(ids, id)
	}
	var out []admin.Organization
	for _, id := range newestFirst(ids, page.After) {
		o := s.Orgs[id]
		if holds(page.Query, o.Slug, o.Name) && len(out) < page.Limit {
			out = append(out, admin.Organization{ID: o.ID, Slug: o.Slug, Name: o.Name, CreatedAt: o.CreatedAt, Members: len(s.Memberships[id])})
		}
	}
	return out, nil
}

// AllUsers lists a page of the users AddUser registered.
func (s *Store) AllUsers(_ context.Context, page admin.Page) ([]admin.User, error) {
	var ids []uuid.UUID
	for id := range s.Names {
		ids = append(ids, id)
	}
	var out []admin.User
	for _, id := range newestFirst(ids, page.After) {
		if !holds(page.Query, append([]string{s.Names[id]}, s.Emails[id]...)...) || len(out) == page.Limit {
			continue
		}
		u := admin.User{ID: id, DisplayName: s.Names[id]}
		for _, e := range s.Emails[id] {
			u.Emails = append(u.Emails, auth.Email{Email: e, Verified: true})
		}
		for _, g := range s.Grants {
			if r, ok := s.role(g.Role); ok && g.Principal == id && g.Scope == auth.ScopeInstallation {
				u.Roles = append(u.Roles, r.Name)
			}
		}
		for _, users := range s.Memberships {
			if slices.Contains(users, id) {
				u.Organizations++
			}
		}
		out = append(out, u)
	}
	return out, nil
}

// VerifiedEmailHolder finds the user AddUser registered with email.
func (s *Store) VerifiedEmailHolder(_ context.Context, email string) (uuid.UUID, error) {
	for id, emails := range s.Emails {
		if slices.Contains(emails, email) {
			return id, nil
		}
	}
	return uuid.Nil, orgs.ErrNotFound
}

// CreateFor records the organization with owner its first Owner.
func (s *Store) CreateFor(ctx context.Context, _, owner uuid.UUID, org orgs.Organization) error {
	return s.Create(ctx, owner, org)
}

// Enter makes actor an Owner of the organization, a member first, and records the entry.
func (s *Store) Enter(_ context.Context, actor, orgID uuid.UUID, reason string, _ time.Time) error {
	if s.Fail != nil {
		return s.Fail
	}
	if _, ok := s.Orgs[orgID]; !ok {
		return orgs.ErrNotFound
	}
	if !slices.Contains(s.Memberships[orgID], actor) {
		s.Memberships[orgID] = append(s.Memberships[orgID], actor)
		s.Events = append(s.Events, "member.joined")
	}
	s.Grant(actor, auth.RoleOwner, auth.ScopeOrganization, orgID)
	s.Entered = append(s.Entered, Entry{Org: orgID, User: actor, Reason: reason})
	s.Events = append(s.Events, "org.entered")
	return nil
}

// InstallationSettings reads the settings the Back-office changes.
func (s *Store) InstallationSettings(context.Context) (auth.InstallationSettings, error) {
	if s.Fail != nil {
		return auth.InstallationSettings{}, s.Fail
	}
	return s.Installation, nil
}

// ChangeInstallationSettings writes settings unless the stored version is no longer theirs, and bumps the version.
func (s *Store) ChangeInstallationSettings(_ context.Context, actor uuid.UUID, settings auth.InstallationSettings, at time.Time) (auth.InstallationSettings, error) {
	if s.Fail != nil {
		return auth.InstallationSettings{}, s.Fail
	}
	if settings.Version != s.Installation.Version {
		return auth.InstallationSettings{}, admin.ErrStale
	}
	settings.Version++
	settings.UpdatedAt, settings.UpdatedBy = at, actor
	s.Installation = settings
	return settings, nil
}

// IdentityProviders lists the Back-office's providers by ID.
func (s *Store) IdentityProviders(context.Context) ([]auth.StoredProvider, error) {
	if s.Fail != nil {
		return nil, s.Fail
	}
	out := make([]auth.StoredProvider, 0, len(s.Providers))
	for _, id := range slices.Sorted(maps.Keys(s.Providers)) {
		out = append(out, s.Providers[id])
	}
	return out, nil
}

// IdentityProvider finds the Back-office's provider id.
func (s *Store) IdentityProvider(_ context.Context, id string) (auth.StoredProvider, error) {
	p, ok := s.Providers[id]
	if !ok {
		return auth.StoredProvider{}, orgs.ErrNotFound
	}
	return p, nil
}

// CreateIdentityProvider adds p at version 1, unless its ID is taken.
func (s *Store) CreateIdentityProvider(_ context.Context, actor uuid.UUID, p auth.StoredProvider, at time.Time) (auth.StoredProvider, error) {
	if s.Fail != nil {
		return auth.StoredProvider{}, s.Fail
	}
	if _, taken := s.Providers[p.ID]; taken {
		return auth.StoredProvider{}, admin.ErrProviderTaken
	}
	p.Version, p.CreatedAt, p.UpdatedAt, p.UpdatedBy = 1, at, at, actor
	s.Providers[p.ID] = p
	return p, nil
}

// ChangeIdentityProvider writes p unless the stored version is no longer its, and bumps the version.
func (s *Store) ChangeIdentityProvider(_ context.Context, actor uuid.UUID, p auth.StoredProvider, at time.Time) (auth.StoredProvider, error) {
	if s.Fail != nil {
		return auth.StoredProvider{}, s.Fail
	}
	if stored, ok := s.Providers[p.ID]; !ok || stored.Version != p.Version {
		return auth.StoredProvider{}, admin.ErrStale
	}
	p.Version++
	p.UpdatedAt, p.UpdatedBy = at, actor
	s.Providers[p.ID] = p
	return p, nil
}

// DeleteIdentityProvider removes provider id unless its version is no longer version.
func (s *Store) DeleteIdentityProvider(_ context.Context, _ uuid.UUID, id string, version int64) error {
	if s.Fail != nil {
		return s.Fail
	}
	if stored, ok := s.Providers[id]; !ok || stored.Version != version {
		return admin.ErrStale
	}
	delete(s.Providers, id)
	return nil
}
