package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// Store is what the database keeps for the installation's administration. Each method that writes is one
// transaction, which acts for actor, so the audit log records who changed each row.
type Store interface {
	// AllOrganizations lists the organizations whose slug or name holds page.Query, in any case, newest first, after
	// page.After, at most page.Limit.
	AllOrganizations(ctx context.Context, page Page) ([]Organization, error)
	// AllUsers lists the users whose display name or an email holds page.Query, in any case, newest first, after
	// page.After, at most page.Limit.
	AllUsers(ctx context.Context, page Page) ([]User, error)
	// BySlug finds an organization by any slug it has had, or orgs.ErrNotFound.
	BySlug(ctx context.Context, slug string) (orgs.Organization, error)
	// VerifiedEmailHolder finds the user who holds email, verified, or orgs.ErrNotFound.
	VerifiedEmailHolder(ctx context.Context, email string) (uuid.UUID, error)
	// CreateFor makes the organization, its slug, owner's membership and Owner grant, and the events, acting for
	// actor; orgs.ErrSlugTaken for a slug another organization has or had.
	CreateFor(ctx context.Context, actor, owner uuid.UUID, org orgs.Organization) error
	// Enter makes actor an Owner of the organization, a member first when they aren't one, with a grant of source
	// auth.SourceEntry, and records the entry with its reason in the organization's trail; orgs.ErrNotFound for an
	// organization that isn't there.
	Enter(ctx context.Context, actor, orgID uuid.UUID, reason string, at time.Time) error
	// InstallationSettings reads the settings the Back-office changes.
	InstallationSettings(ctx context.Context) (auth.InstallationSettings, error)
	// ChangeInstallationSettings writes settings at at, acting for actor, unless the stored version is no longer
	// settings.Version: then ErrStale. It returns the settings as written, their version bumped.
	ChangeInstallationSettings(ctx context.Context, actor uuid.UUID, settings auth.InstallationSettings, at time.Time) (auth.InstallationSettings, error)
	// IdentityProviders lists the identity providers the Back-office added, by ID.
	IdentityProviders(ctx context.Context) ([]auth.StoredProvider, error)
	// IdentityProvider finds the Back-office's provider id, or orgs.ErrNotFound.
	IdentityProvider(ctx context.Context, id string) (auth.StoredProvider, error)
	// CreateIdentityProvider adds p at at, acting for actor, or ErrProviderTaken for an ID another has. It returns the
	// provider as written.
	CreateIdentityProvider(ctx context.Context, actor uuid.UUID, p auth.StoredProvider, at time.Time) (auth.StoredProvider, error)
	// ChangeIdentityProvider writes p at at, acting for actor, unless the stored version is no longer p.Version: then
	// ErrStale. It returns the provider as written, its version bumped.
	ChangeIdentityProvider(ctx context.Context, actor uuid.UUID, p auth.StoredProvider, at time.Time) (auth.StoredProvider, error)
	// DeleteIdentityProvider removes provider id, acting for actor, unless its version is no longer version: then
	// ErrStale.
	DeleteIdentityProvider(ctx context.Context, actor uuid.UUID, id string, version int64) error
}

// Service runs the installation's administration: every operation checks its viewer's permissions at the
// installation through the authorizer first.
type Service struct {
	store Store
	authz *auth.Authorizer
	// values are the chart's sign-up settings, which lock those they set.
	values auth.Settings
	// providers is what the identity providers' administration needs (SetProviders).
	providers ProviderSetup
	now       func() time.Time
}

// NewService returns a Service over store, checking permissions with authz, under the chart's values' sign-up settings.
func NewService(store Store, authz *auth.Authorizer, values auth.Settings) *Service {
	return &Service{store: store, authz: authz, values: values, now: time.Now}
}

// SetClock makes the service read the time from now: for tests.
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

// Permissions lists what v holds at the installation, for the Back-office to show what v may do.
func (s *Service) Permissions(ctx context.Context, v auth.User) ([]string, error) {
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeInstallation, uuid.Nil)
	if err != nil {
		return nil, fmt.Errorf("reading the viewer's permissions: %w", err)
	}
	return held, nil
}

// require returns auth.ErrForbidden unless v holds every one of permissions at the installation.
func (s *Service) require(ctx context.Context, v auth.User, permissions ...string) error {
	held, err := s.Permissions(ctx, v)
	if err != nil {
		return err
	}
	if !auth.Covers(held, permissions) {
		return fmt.Errorf("%w: %s at the installation", auth.ErrForbidden, strings.Join(permissions, " and "))
	}
	return nil
}

// Organizations lists a page of the installation's organizations; it takes installation.manage.
func (s *Service) Organizations(ctx context.Context, v auth.User, p Page) (List[Organization], error) {
	if err := s.require(ctx, v, PermManage); err != nil {
		return List[Organization]{}, err
	}
	p = p.normalize()
	items, err := s.store.AllOrganizations(ctx, Page{Query: p.Query, After: p.After, Limit: p.Limit + 1})
	if err != nil {
		return List[Organization]{}, fmt.Errorf("listing the organizations: %w", err)
	}
	return page(items, p.Limit, func(o Organization) uuid.UUID { return o.ID }), nil
}

// Users lists a page of the installation's users; it takes installation.users.manage.
func (s *Service) Users(ctx context.Context, v auth.User, p Page) (List[User], error) {
	if err := s.require(ctx, v, PermUsersManage); err != nil {
		return List[User]{}, err
	}
	p = p.normalize()
	items, err := s.store.AllUsers(ctx, Page{Query: p.Query, After: p.After, Limit: p.Limit + 1})
	if err != nil {
		return List[User]{}, fmt.Errorf("listing the users: %w", err)
	}
	return page(items, p.Limit, func(u User) uuid.UUID { return u.ID }), nil
}

// CreateOrganization makes an organization with slug and name whose first Owner is the user who holds ownerEmail,
// verified; v doesn't join it. It takes orgs.create, and installation.users.manage to find any user by email.
func (s *Service) CreateOrganization(ctx context.Context, v auth.User, slug, name, ownerEmail string) (Organization, error) {
	if err := s.require(ctx, v, orgs.PermCreate, PermUsersManage); err != nil {
		return Organization{}, err
	}
	org, err := orgs.NewOrganization(slug, name, s.now())
	ownerEmail = strings.ToLower(strings.TrimSpace(ownerEmail))
	if orgs.ValidateEmail(ownerEmail) != nil {
		err = errors.Join(err, &orgs.ValidationError{Field: "ownerEmail", Code: "email.invalid", Message: "not an email address"})
	}
	if err != nil {
		return Organization{}, err
	}
	owner, err := s.store.VerifiedEmailHolder(ctx, ownerEmail)
	if errors.Is(err, orgs.ErrNotFound) {
		return Organization{}, &orgs.ValidationError{Field: "ownerEmail", Code: "owner.not_found", Message: "no user holds this email, verified"}
	}
	if err != nil {
		return Organization{}, fmt.Errorf("finding the owner: %w", err)
	}
	if err := s.store.CreateFor(ctx, v.ID, owner, org); err != nil {
		if errors.Is(err, orgs.ErrSlugTaken) {
			return Organization{}, orgs.ErrSlugTaken
		}
		return Organization{}, fmt.Errorf("creating the organization: %w", err)
	}
	return Organization{ID: org.ID, Slug: org.Slug, Name: org.Name, CreatedAt: org.CreatedAt, Members: 1}, nil
}

// Enter makes v an Owner of the organization slug names, for reason, which its Owners read in its audit trail; it
// takes installation.manage. v, already an Owner, gains nothing, and the entry is still recorded.
func (s *Service) Enter(ctx context.Context, v auth.User, slug, reason string) (orgs.Organization, error) {
	if err := s.require(ctx, v, PermManage); err != nil {
		return orgs.Organization{}, err
	}
	reason = strings.TrimSpace(reason)
	if err := validateReason(reason); err != nil {
		return orgs.Organization{}, err
	}
	org, err := s.store.BySlug(ctx, slug)
	if errors.Is(err, orgs.ErrNotFound) {
		return orgs.Organization{}, orgs.ErrNotFound
	}
	if err != nil {
		return orgs.Organization{}, fmt.Errorf("finding the organization: %w", err)
	}
	if err := s.store.Enter(ctx, v.ID, org.ID, reason, s.now()); err != nil {
		if errors.Is(err, orgs.ErrNotFound) {
			return orgs.Organization{}, orgs.ErrNotFound
		}
		return orgs.Organization{}, fmt.Errorf("entering the organization: %w", err)
	}
	return org, nil
}
