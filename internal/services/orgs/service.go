package orgs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/limits"
)

// Store is what the database keeps for organizations. Each method is one transaction, which acts for actor where
// one is named, so the audit log records who changed each row.
type Store interface {
	// Create makes the organization, its slug, its creator's membership and their Owner grant, and the events, and
	// counts it against its creator. Under a lock of the creator's, it refuses with a *limits.ExceededError a creator
	// who has created limit organizations already; 0 caps nothing.
	Create(ctx context.Context, actor uuid.UUID, org Organization, limit int64) error
	// BySlug finds an organization by any slug it has had, or ErrNotFound.
	BySlug(ctx context.Context, slug string) (Organization, error)
	// ByID finds an organization, or ErrNotFound.
	ByID(ctx context.Context, id uuid.UUID) (Organization, error)
	// Update writes the organization's settings, claiming its slug when it changed, or ErrSlugTaken.
	Update(ctx context.Context, actor uuid.UUID, org Organization) error
	// OrganizationsOf lists the organizations user belongs to, by name.
	OrganizationsOf(ctx context.Context, userID uuid.UUID) ([]Organization, error)
	// Members lists the organization's members with the roles they hold there, by display name.
	Members(ctx context.Context, orgID uuid.UUID) ([]Member, error)
	// Member finds one member with their roles, or ErrNotFound.
	Member(ctx context.Context, orgID, userID uuid.UUID) (Member, error)
	// MemberByEmail finds the member who holds email, verified, or ErrNotFound.
	MemberByEmail(ctx context.Context, orgID uuid.UUID, email string) (uuid.UUID, error)
	// Leave drops the member and every role they hold at the organization, with the pending invitations to their
	// emails and their pending requests there; actor is who did it. The organization's last Owner can't go:
	// ErrLastOwner, decided under a lock, so two Owners leaving at once don't both.
	Leave(ctx context.Context, actor uuid.UUID, orgID, userID uuid.UUID, at time.Time) error
	// Invite records the invitation, or ErrInvited for an email already invited.
	Invite(ctx context.Context, actor uuid.UUID, inv Invitation) error
	// Invitation finds an invitation, or ErrNotFound.
	Invitation(ctx context.Context, id uuid.UUID) (Invitation, error)
	// Invitations lists the organization's pending invitations at now, oldest first.
	Invitations(ctx context.Context, orgID uuid.UUID, now time.Time) ([]Invitation, error)
	// InvitationsFor lists the pending invitations to any of emails at now, oldest first.
	InvitationsFor(ctx context.Context, emails []string, now time.Time) ([]Invitation, error)
	// Accept makes the invitee a member with the invitation's role, closes the invitation, and records both.
	Accept(ctx context.Context, actor uuid.UUID, inv Invitation, userID uuid.UUID, at time.Time) error
	// CloseInvitation ends a pending invitation as state, or ErrNotFound when it isn't pending.
	CloseInvitation(ctx context.Context, actor uuid.UUID, id uuid.UUID, state State, at time.Time) error
	// Request records the join request, or ErrRequested for a user who already asked.
	Request(ctx context.Context, actor uuid.UUID, req JoinRequest) error
	// JoinRequest finds a join request, or ErrNotFound.
	JoinRequest(ctx context.Context, id uuid.UUID) (JoinRequest, error)
	// JoinRequests lists the organization's pending requests at now, oldest first.
	JoinRequests(ctx context.Context, orgID uuid.UUID, now time.Time) ([]JoinRequest, error)
	// JoinRequestsOf lists the user's pending requests at now, oldest first.
	JoinRequestsOf(ctx context.Context, userID uuid.UUID, now time.Time) ([]JoinRequest, error)
	// Approve makes the requester a member with roleID, closes the request, and records both.
	Approve(ctx context.Context, actor uuid.UUID, req JoinRequest, roleID uuid.UUID, at time.Time) error
	// CloseJoinRequest ends a pending request as state, or ErrNotFound when it isn't pending.
	CloseJoinRequest(ctx context.Context, actor uuid.UUID, id uuid.UUID, state State, at time.Time) error
	// Role finds a role, or ErrNotFound.
	Role(ctx context.Context, id uuid.UUID) (Role, error)
	// Roles lists the roles granted at the organization: the built-in organization roles first, then its own;
	// uuid.Nil lists the built-in ones alone.
	Roles(ctx context.Context, orgID uuid.UUID) ([]Role, error)
	// CreateRole records a custom role of role.OrgID, or ErrRoleNameTaken.
	CreateRole(ctx context.Context, actor uuid.UUID, role Role) error
	// UpdateRole rewrites one of role.OrgID's own roles: ErrNotFound for a built-in or another organization's,
	// ErrRoleNameTaken for a name another role there has.
	UpdateRole(ctx context.Context, actor uuid.UUID, role Role) error
	// DeleteRole deletes one of the organization's own roles and cancels the invitations that offered it, at `at`:
	// ErrRoleInUse while somebody holds it or an organization gives it to everyone who joins.
	DeleteRole(ctx context.Context, actor uuid.UUID, orgID, roleID uuid.UUID, at time.Time) error
	// GrantRole gives the member the role at the organization, or ErrGranted when they hold it.
	GrantRole(ctx context.Context, actor uuid.UUID, orgID, userID, roleID uuid.UUID) error
	// RevokeRole takes the role from the member at the organization: ErrNotFound when they don't hold it, and
	// ErrLastOwner for the Owner role of the organization's one Owner, decided under a lock.
	RevokeRole(ctx context.Context, actor uuid.UUID, orgID, userID, roleID uuid.UUID) error
	// APIKeys lists the organization's API keys with their roles and secrets, by name.
	APIKeys(ctx context.Context, orgID uuid.UUID) ([]APIKey, error)
	// APIKey finds one of the organization's API keys with its roles and secrets, or ErrNotFound.
	APIKey(ctx context.Context, orgID, id uuid.UUID) (APIKey, error)
	// CreateAPIKey makes the key's principal, grants it key.Roles at the organization, records its first secret
	// (hash is what the database keeps of it) and the event, under the organization's lock.
	CreateAPIKey(ctx context.Context, actor uuid.UUID, key APIKey, secret Secret, hash []byte) error
	// AddSecret gives the key another secret, or ErrTwoSecrets while two act at `at`, decided under the
	// organization's lock; ErrNotFound for a key that isn't the organization's.
	AddSecret(ctx context.Context, actor uuid.UUID, orgID, keyID uuid.UUID, secret Secret, hash []byte, at time.Time) error
	// RevokeSecret revokes one of the key's secrets at `at`, or ErrNotFound when it isn't the key's or is revoked.
	RevokeSecret(ctx context.Context, actor uuid.UUID, orgID, keyID, secretID uuid.UUID, at time.Time) error
	// DeleteAPIKey deletes the key with its principal, secrets and grants, or ErrNotFound.
	DeleteAPIKey(ctx context.Context, actor uuid.UUID, orgID, keyID uuid.UUID) error
	// DeleteExpired deletes the invitations and join requests that expired before now, and returns how many of each.
	DeleteExpired(ctx context.Context, now time.Time) (invitations, requests int64, err error)
}

// The permissions this slice checks.
const (
	PermCreate        = "orgs.create"
	PermRead          = "org.read"
	PermManage        = "org.manage"
	PermMembersManage = "org.members.manage"
	PermGrantsManage  = "org.grants.manage"
	// PermPrincipalsManage makes, rotates and deletes the organization's API keys.
	PermPrincipalsManage = "org.principals.manage"
)

// Limits says which limits apply: limits.Service.
type Limits interface {
	Installation(ctx context.Context) (limits.Limits, error)
}

// Service runs organizations: every operation checks its viewer's permissions through the authorizer first.
type Service struct {
	store  Store
	authz  *auth.Authorizer
	limits Limits
	now    func() time.Time
}

// NewService returns a Service over store, checking permissions with authz.
func NewService(store Store, authz *auth.Authorizer) *Service {
	return &Service{store: store, authz: authz, now: time.Now}
}

// SetClock makes the service read the time from now: for tests.
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

// WithLimits caps the organizations each user creates with the limits l says apply; without it, nothing caps them.
func (s *Service) WithLimits(l Limits) *Service {
	s.limits = l
	return s
}

// Viewer is who an operation runs for: a signed-in user, with the emails they hold.
type Viewer = auth.User

// verifiedEmails lists the viewer's verified emails.
func verifiedEmails(v Viewer) []string {
	var emails []string
	for _, e := range v.Emails {
		if e.Verified {
			emails = append(emails, e.Email)
		}
	}
	return emails
}

// Permissions lists what v holds at the installation.
func (s *Service) Permissions(ctx context.Context, v Viewer) ([]string, error) {
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeInstallation, uuid.Nil)
	if err != nil {
		return nil, fmt.Errorf("reading the viewer's permissions: %w", err)
	}
	return held, nil
}

// Create makes an organization with slug and name, its creator v its first Owner; it takes orgs.create, and is
// refused with a *limits.ExceededError once v has created as many organizations as a user may.
func (s *Service) Create(ctx context.Context, v Viewer, slug, name string) (Organization, error) {
	if err := s.authz.Can(ctx, v.ID, PermCreate, auth.ScopeInstallation, uuid.Nil); err != nil {
		return Organization{}, fmt.Errorf("checking the viewer: %w", err)
	}
	org, err := NewOrganization(slug, name, s.now())
	if err != nil {
		return Organization{}, err
	}
	var limit int64
	if s.limits != nil {
		l, err := s.limits.Installation(ctx)
		if err != nil {
			return Organization{}, fmt.Errorf("reading the limits: %w", err)
		}
		limit = l.OrganizationsPerUser
	}
	if err := s.store.Create(ctx, v.ID, org, limit); err != nil {
		return Organization{}, fmt.Errorf("creating the organization: %w", err)
	}
	return org, nil
}

// Organizations lists the organizations v belongs to.
func (s *Service) Organizations(ctx context.Context, v Viewer) ([]Organization, error) {
	orgs, err := s.store.OrganizationsOf(ctx, v.ID)
	if err != nil {
		return nil, fmt.Errorf("listing the organizations: %w", err)
	}
	return orgs, nil
}

// Get finds the organization slug names, by its current slug or an old one, for a viewer with org.read there;
// anyone else gets ErrNotFound, so nobody learns that an organization they can't see exists.
func (s *Service) Get(ctx context.Context, v Viewer, slug string) (Organization, error) {
	org, err := s.store.BySlug(ctx, slug)
	if err != nil {
		return Organization{}, wrap("finding the organization", err)
	}
	if _, err := s.read(ctx, v, org.ID); err != nil {
		return Organization{}, err
	}
	return org, nil
}

// read returns the permissions v holds at the organization, or ErrNotFound without org.read.
func (s *Service) read(ctx context.Context, v Viewer, orgID uuid.UUID) ([]string, error) {
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeOrganization, orgID)
	if err != nil {
		return nil, fmt.Errorf("reading the viewer's permissions: %w", err)
	}
	if !slices.Contains(held, PermRead) {
		return nil, ErrNotFound
	}
	return held, nil
}

// require returns the organization and the permissions v holds there, ErrNotFound without org.read and
// auth.ErrForbidden without permission.
func (s *Service) require(ctx context.Context, v Viewer, orgID uuid.UUID, permission string) (Organization, []string, error) {
	org, err := s.store.ByID(ctx, orgID)
	if err != nil {
		return Organization{}, nil, wrap("finding the organization", err)
	}
	held, err := s.read(ctx, v, orgID)
	if err != nil {
		return Organization{}, nil, err
	}
	if !slices.Contains(held, permission) {
		return Organization{}, nil, fmt.Errorf("%w: %s at the organization", auth.ErrForbidden, permission)
	}
	return org, held, nil
}

// ViewerPermissions lists what v holds at the organization; ErrNotFound without org.read.
func (s *Service) ViewerPermissions(ctx context.Context, v Viewer, orgID uuid.UUID) ([]string, error) {
	return s.read(ctx, v, orgID)
}

// Update writes the organization's settings; it takes org.manage.
func (s *Service) Update(ctx context.Context, v Viewer, orgID uuid.UUID, settings Settings) (Organization, error) {
	org, _, err := s.require(ctx, v, orgID, PermManage)
	if err != nil {
		return Organization{}, err
	}
	settings.Name = strings.TrimSpace(settings.Name)
	if err := errors.Join(ValidateSlug(settings.Slug), ValidateName(settings.Name)); err != nil {
		return Organization{}, err
	}
	if _, err := s.organizationRole(ctx, orgID, settings.MembershipRoleID); err != nil {
		return Organization{}, err
	}
	org.Slug, org.Name, org.MembershipRoleID = settings.Slug, settings.Name, settings.MembershipRoleID
	org.AcceptsJoinRequests, org.Providers = settings.AcceptsJoinRequests, settings.Providers
	if len(org.Providers) == 0 {
		org.Providers = nil // all of the installation's, however a client spells it
	}
	if err := s.store.Update(ctx, v.ID, org); err != nil {
		return Organization{}, fmt.Errorf("updating the organization: %w", err)
	}
	return org, nil
}

// organizationRole finds roleID among the roles granted at the organization.
func (s *Service) organizationRole(ctx context.Context, orgID, roleID uuid.UUID) (Role, error) {
	roles, err := s.store.Roles(ctx, orgID)
	if err != nil {
		return Role{}, fmt.Errorf("listing the roles: %w", err)
	}
	for _, r := range roles {
		if r.ID == roleID {
			return r, nil
		}
	}
	return Role{}, &ValidationError{Field: "roleId", Code: "role.invalid", Message: ErrNotOrganizationRole.Error()}
}

// Roles lists the roles the organization grants, the built-in ones first; it takes org.read.
func (s *Service) Roles(ctx context.Context, v Viewer, orgID uuid.UUID) ([]Role, error) {
	if _, _, err := s.require(ctx, v, orgID, PermRead); err != nil {
		return nil, err
	}
	roles, err := s.store.Roles(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing the roles: %w", err)
	}
	return roles, nil
}

// BuiltInRoles lists the built-in organization roles, for any signed-in viewer: what GET /v1/roles answered before
// roles became an organization's, kept while that route is deprecated.
func (s *Service) BuiltInRoles(ctx context.Context) ([]Role, error) {
	roles, err := s.store.Roles(ctx, uuid.Nil)
	if err != nil {
		return nil, fmt.Errorf("listing the built-in roles: %w", err)
	}
	return roles, nil
}

// covers applies the grant rules to permissions: the viewer, who holds held, grants or revokes nothing they lack.
func covers(held, permissions []string, what string) error {
	if !auth.Covers(held, permissions) {
		return fmt.Errorf("%w: %s takes every permission involved", auth.ErrForbidden, what)
	}
	return nil
}

// nameFree refuses a role's name another role granted at the organization has, in any case, but the role itself.
func (s *Service) nameFree(ctx context.Context, orgID, self uuid.UUID, name string) error {
	roles, err := s.store.Roles(ctx, orgID)
	if err != nil {
		return fmt.Errorf("listing the roles: %w", err)
	}
	for _, r := range roles {
		if r.ID != self && strings.EqualFold(r.Name, name) {
			return ErrRoleNameTaken
		}
	}
	return nil
}

// CreateRole defines a custom role of the organization; it takes org.grants.manage and every permission the role
// lists.
func (s *Service) CreateRole(ctx context.Context, v Viewer, orgID uuid.UUID, in RoleInput) (Role, error) {
	_, held, err := s.require(ctx, v, orgID, PermGrantsManage)
	if err != nil {
		return Role{}, err
	}
	in, err = ValidateRole(in)
	if err != nil {
		return Role{}, err
	}
	if err := covers(held, in.Permissions, "defining a role"); err != nil {
		return Role{}, err
	}
	if err := s.nameFree(ctx, orgID, uuid.Nil, in.Name); err != nil {
		return Role{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Role{}, fmt.Errorf("making an ID: %w", err)
	}
	role := Role{ID: id, Name: in.Name, Description: in.Description, Permissions: in.Permissions, OrgID: orgID}
	if err := s.store.CreateRole(ctx, v.ID, role); err != nil {
		return Role{}, wrap("creating the role", err)
	}
	return role, nil
}

// customRole finds roleID among the organization's own roles: ErrNotFound for one it hasn't, ErrBuiltInRole for a
// built-in one.
func (s *Service) customRole(ctx context.Context, orgID, roleID uuid.UUID) (Role, error) {
	role, err := s.organizationRole(ctx, orgID, roleID)
	if err != nil {
		return Role{}, ErrNotFound
	}
	if role.BuiltIn {
		return Role{}, ErrBuiltInRole
	}
	return role, nil
}

// UpdateRole redefines one of the organization's own roles, and with it every grant of it; it takes
// org.grants.manage and every permission the role listed or lists.
func (s *Service) UpdateRole(ctx context.Context, v Viewer, orgID, roleID uuid.UUID, in RoleInput) (Role, error) {
	_, held, err := s.require(ctx, v, orgID, PermGrantsManage)
	if err != nil {
		return Role{}, err
	}
	role, err := s.customRole(ctx, orgID, roleID)
	if err != nil {
		return Role{}, err
	}
	in, err = ValidateRole(in)
	if err != nil {
		return Role{}, err
	}
	if err := covers(held, slices.Concat(role.Permissions, in.Permissions), "changing a role"); err != nil {
		return Role{}, err
	}
	if err := s.nameFree(ctx, orgID, roleID, in.Name); err != nil {
		return Role{}, err
	}
	role.Name, role.Description, role.Permissions = in.Name, in.Description, in.Permissions
	if err := s.store.UpdateRole(ctx, v.ID, role); err != nil {
		return Role{}, wrap("updating the role", err)
	}
	return role, nil
}

// DeleteRole deletes one of the organization's own roles that nobody holds; it takes org.grants.manage and every
// permission of the role. The membership role stays until the settings name another.
func (s *Service) DeleteRole(ctx context.Context, v Viewer, orgID, roleID uuid.UUID) error {
	org, held, err := s.require(ctx, v, orgID, PermGrantsManage)
	if err != nil {
		return err
	}
	role, err := s.customRole(ctx, orgID, roleID)
	if err != nil {
		return err
	}
	if err := covers(held, role.Permissions, "deleting a role"); err != nil {
		return err
	}
	if org.MembershipRoleID == roleID {
		return ErrRoleInUse
	}
	return wrap("deleting the role", s.store.DeleteRole(ctx, v.ID, orgID, roleID, s.now()))
}

// grantable finds the role and the member a grant or a revocation names, for a viewer with org.grants.manage who
// holds every permission of the role.
func (s *Service) grantable(ctx context.Context, v Viewer, orgID, userID, roleID uuid.UUID, what string) (Member, Role, error) {
	_, held, err := s.require(ctx, v, orgID, PermGrantsManage)
	if err != nil {
		return Member{}, Role{}, err
	}
	role, err := s.organizationRole(ctx, orgID, roleID)
	if err != nil {
		return Member{}, Role{}, err
	}
	if err := covers(held, role.Permissions, what); err != nil {
		return Member{}, Role{}, err
	}
	member, err := s.store.Member(ctx, orgID, userID)
	if err != nil {
		return Member{}, Role{}, wrap("finding the member", err)
	}
	return member, role, nil
}

// holdsRole says whether the member holds roleID.
func holdsRole(m Member, roleID uuid.UUID) bool {
	return slices.ContainsFunc(m.Roles, func(r Role) bool { return r.ID == roleID })
}

// GrantRole gives a member a role at the organization; it takes org.grants.manage and every permission of the
// role.
func (s *Service) GrantRole(ctx context.Context, v Viewer, orgID, userID, roleID uuid.UUID) error {
	member, role, err := s.grantable(ctx, v, orgID, userID, roleID, "granting "+roleID.String())
	if err != nil {
		return err
	}
	if holdsRole(member, role.ID) {
		return ErrGranted
	}
	return wrap("granting the role", s.store.GrantRole(ctx, v.ID, orgID, userID, role.ID))
}

// RevokeRole takes a role from a member at the organization; it takes org.grants.manage and every permission of
// the role. The Owner role stays with the organization's last Owner.
func (s *Service) RevokeRole(ctx context.Context, v Viewer, orgID, userID, roleID uuid.UUID) error {
	member, role, err := s.grantable(ctx, v, orgID, userID, roleID, "revoking "+roleID.String())
	if err != nil {
		return err
	}
	if !holdsRole(member, role.ID) {
		return ErrNotFound
	}
	return wrap("revoking the role", s.store.RevokeRole(ctx, v.ID, orgID, userID, role.ID))
}

// Members lists the organization's members with their roles; it takes org.read.
func (s *Service) Members(ctx context.Context, v Viewer, orgID uuid.UUID) ([]Member, error) {
	if _, _, err := s.require(ctx, v, orgID, PermRead); err != nil {
		return nil, err
	}
	members, err := s.store.Members(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing the members: %w", err)
	}
	return members, nil
}

// mayGrant applies the grant rules: giving role at the organization takes org.grants.manage and every permission
// of the role, unless role is the organization's membership role, which org.members.manage alone gives.
func mayGrant(org Organization, held []string, role Role) error {
	if role.ID == org.MembershipRoleID {
		return nil
	}
	if !slices.Contains(held, PermGrantsManage) || !auth.Covers(held, role.Permissions) {
		return fmt.Errorf("%w: granting %s takes %s and every permission of the role", auth.ErrForbidden, role.Name, PermGrantsManage)
	}
	return nil
}

// Invite invites email to the organization with roleID (uuid.Nil for the membership role); it takes
// org.members.manage, and another role than the membership role adds the grant rules.
func (s *Service) Invite(ctx context.Context, v Viewer, orgID uuid.UUID, email string, roleID uuid.UUID) (Invitation, error) {
	org, held, err := s.require(ctx, v, orgID, PermMembersManage)
	if err != nil {
		return Invitation{}, err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if err := ValidateEmail(email); err != nil {
		return Invitation{}, err
	}
	if roleID == uuid.Nil {
		roleID = org.MembershipRoleID
	}
	role, err := s.organizationRole(ctx, orgID, roleID)
	if err != nil {
		return Invitation{}, err
	}
	if err := mayGrant(org, held, role); err != nil {
		return Invitation{}, err
	}
	if _, err := s.store.MemberByEmail(ctx, orgID, email); err == nil {
		return Invitation{}, ErrAlreadyMember
	} else if !errors.Is(err, ErrNotFound) {
		return Invitation{}, fmt.Errorf("looking the email up: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Invitation{}, fmt.Errorf("making an ID: %w", err)
	}
	now := s.now()
	inv := Invitation{ID: id, OrgID: orgID, OrgSlug: org.Slug, OrgName: org.Name, Email: email, RoleID: role.ID, RoleName: role.Name, InvitedBy: v.ID,
		State: StatePending, CreatedAt: now, ExpiresAt: now.Add(InvitationLifetime)}
	if err := s.store.Invite(ctx, v.ID, inv); err != nil {
		return Invitation{}, wrap("recording the invitation", err)
	}
	return inv, nil
}

// Invitations lists the organization's pending invitations; it takes org.members.manage.
func (s *Service) Invitations(ctx context.Context, v Viewer, orgID uuid.UUID) ([]Invitation, error) {
	if _, _, err := s.require(ctx, v, orgID, PermMembersManage); err != nil {
		return nil, err
	}
	invitations, err := s.store.Invitations(ctx, orgID, s.now())
	if err != nil {
		return nil, fmt.Errorf("listing the invitations: %w", err)
	}
	return invitations, nil
}

// CancelInvitation ends a pending invitation of the organization; it takes org.members.manage.
func (s *Service) CancelInvitation(ctx context.Context, v Viewer, orgID, id uuid.UUID) error {
	if _, _, err := s.require(ctx, v, orgID, PermMembersManage); err != nil {
		return err
	}
	inv, err := s.pendingInvitation(ctx, id)
	if err != nil || inv.OrgID != orgID {
		return ErrNotFound
	}
	return wrap("cancelling the invitation", s.store.CloseInvitation(ctx, v.ID, id, StateCancelled, s.now()))
}

// pendingInvitation finds invitation id if it still waits.
func (s *Service) pendingInvitation(ctx context.Context, id uuid.UUID) (Invitation, error) {
	inv, err := s.store.Invitation(ctx, id)
	if err != nil {
		return Invitation{}, wrap("finding the invitation", err)
	}
	if inv.State != StatePending || !s.now().Before(inv.ExpiresAt) {
		return Invitation{}, ErrNotFound
	}
	return inv, nil
}

// MyInvitations lists the pending invitations to v's verified emails.
func (s *Service) MyInvitations(ctx context.Context, v Viewer) ([]Invitation, error) {
	emails := verifiedEmails(v)
	if len(emails) == 0 {
		return nil, nil
	}
	invitations, err := s.store.InvitationsFor(ctx, emails, s.now())
	if err != nil {
		return nil, fmt.Errorf("listing the invitations: %w", err)
	}
	return invitations, nil
}

// myInvitation finds pending invitation id if it names one of v's verified emails; ErrNotFound otherwise.
func (s *Service) myInvitation(ctx context.Context, v Viewer, id uuid.UUID) (Invitation, error) {
	inv, err := s.pendingInvitation(ctx, id)
	if err != nil {
		return Invitation{}, err
	}
	if !slices.Contains(verifiedEmails(v), inv.Email) {
		return Invitation{}, ErrNotFound
	}
	return inv, nil
}

// AcceptInvitation makes v a member through invitation id, which must name one of their verified emails. The
// inviter is checked again: one who lost org.members.manage, or the grant rules for the role, adds nobody.
func (s *Service) AcceptInvitation(ctx context.Context, v Viewer, id uuid.UUID) (Organization, error) {
	inv, err := s.myInvitation(ctx, v, id)
	if err != nil {
		return Organization{}, err
	}
	org, err := s.store.ByID(ctx, inv.OrgID)
	if err != nil {
		return Organization{}, wrap("finding the organization", err)
	}
	role, err := s.store.Role(ctx, inv.RoleID)
	if err != nil {
		return Organization{}, wrap("finding the role", err)
	}
	held, err := s.authz.Holds(ctx, inv.InvitedBy, auth.ScopeOrganization, org.ID)
	if err != nil {
		return Organization{}, fmt.Errorf("reading the inviter's permissions: %w", err)
	}
	if !slices.Contains(held, PermMembersManage) || mayGrant(org, held, role) != nil {
		return Organization{}, ErrInviterGone
	}
	if _, err := s.store.Member(ctx, org.ID, v.ID); err == nil {
		return Organization{}, ErrAlreadyMember
	} else if !errors.Is(err, ErrNotFound) {
		return Organization{}, fmt.Errorf("checking the membership: %w", err)
	}
	if err := s.store.Accept(ctx, v.ID, inv, v.ID, s.now()); err != nil {
		return Organization{}, wrap("accepting the invitation", err)
	}
	return org, nil
}

// DeclineInvitation ends invitation id, which must name one of v's verified emails.
func (s *Service) DeclineInvitation(ctx context.Context, v Viewer, id uuid.UUID) error {
	if _, err := s.myInvitation(ctx, v, id); err != nil {
		return err
	}
	return wrap("declining the invitation", s.store.CloseInvitation(ctx, v.ID, id, StateDeclined, s.now()))
}

// RequestToJoin asks to join the organization slug names, with a note for its managers. An organization that takes
// no join requests answers like one that doesn't exist.
func (s *Service) RequestToJoin(ctx context.Context, v Viewer, slug, note string) (JoinRequest, error) {
	org, err := s.store.BySlug(ctx, slug)
	if err != nil {
		return JoinRequest{}, wrap("finding the organization", err)
	}
	if !org.AcceptsJoinRequests {
		return JoinRequest{}, ErrNotFound
	}
	if _, err := s.store.Member(ctx, org.ID, v.ID); err == nil {
		return JoinRequest{}, ErrAlreadyMember
	} else if !errors.Is(err, ErrNotFound) {
		return JoinRequest{}, fmt.Errorf("checking the membership: %w", err)
	}
	note = strings.TrimSpace(note)
	if len(note) > 500 {
		return JoinRequest{}, &ValidationError{Field: "note", Code: "note.invalid", Message: "a note is at most 500 characters"}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return JoinRequest{}, fmt.Errorf("making an ID: %w", err)
	}
	now := s.now()
	req := JoinRequest{ID: id, OrgID: org.ID, OrgSlug: org.Slug, OrgName: org.Name, UserID: v.ID, UserName: v.DisplayName,
		Note: note, State: StatePending, CreatedAt: now, ExpiresAt: now.Add(JoinRequestLifetime)}
	if err := s.store.Request(ctx, v.ID, req); err != nil {
		return JoinRequest{}, wrap("recording the request", err)
	}
	return req, nil
}

// MyJoinRequests lists v's pending requests.
func (s *Service) MyJoinRequests(ctx context.Context, v Viewer) ([]JoinRequest, error) {
	requests, err := s.store.JoinRequestsOf(ctx, v.ID, s.now())
	if err != nil {
		return nil, fmt.Errorf("listing the requests: %w", err)
	}
	return requests, nil
}

// JoinRequests lists the organization's pending requests; it takes org.members.manage.
func (s *Service) JoinRequests(ctx context.Context, v Viewer, orgID uuid.UUID) ([]JoinRequest, error) {
	if _, _, err := s.require(ctx, v, orgID, PermMembersManage); err != nil {
		return nil, err
	}
	requests, err := s.store.JoinRequests(ctx, orgID, s.now())
	if err != nil {
		return nil, fmt.Errorf("listing the requests: %w", err)
	}
	return requests, nil
}

// pendingRequest finds request id of the organization if it still waits.
func (s *Service) pendingRequest(ctx context.Context, orgID, id uuid.UUID) (JoinRequest, error) {
	req, err := s.store.JoinRequest(ctx, id)
	if err != nil {
		return JoinRequest{}, wrap("finding the request", err)
	}
	if req.OrgID != orgID || req.State != StatePending || !s.now().Before(req.ExpiresAt) {
		return JoinRequest{}, ErrNotFound
	}
	return req, nil
}

// ApproveJoinRequest makes the requester a member with roleID (uuid.Nil for the membership role); it takes
// org.members.manage, and another role than the membership role adds the grant rules.
func (s *Service) ApproveJoinRequest(ctx context.Context, v Viewer, orgID, id, roleID uuid.UUID) error {
	org, held, err := s.require(ctx, v, orgID, PermMembersManage)
	if err != nil {
		return err
	}
	req, err := s.pendingRequest(ctx, orgID, id)
	if err != nil {
		return err
	}
	if roleID == uuid.Nil {
		roleID = org.MembershipRoleID
	}
	role, err := s.organizationRole(ctx, orgID, roleID)
	if err != nil {
		return err
	}
	if err := mayGrant(org, held, role); err != nil {
		return err
	}
	if _, err := s.store.Member(ctx, orgID, req.UserID); err == nil {
		return ErrAlreadyMember
	} else if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("checking the membership: %w", err)
	}
	return wrap("approving the request", s.store.Approve(ctx, v.ID, req, role.ID, s.now()))
}

// DeclineJoinRequest ends a pending request of the organization; it takes org.members.manage.
func (s *Service) DeclineJoinRequest(ctx context.Context, v Viewer, orgID, id uuid.UUID) error {
	if _, _, err := s.require(ctx, v, orgID, PermMembersManage); err != nil {
		return err
	}
	if _, err := s.pendingRequest(ctx, orgID, id); err != nil {
		return err
	}
	return wrap("declining the request", s.store.CloseJoinRequest(ctx, v.ID, id, StateDeclined, s.now()))
}

// Leave drops v from the organization, with every role they hold there; the last Owner can't.
func (s *Service) Leave(ctx context.Context, v Viewer, orgID uuid.UUID) error {
	if _, err := s.read(ctx, v, orgID); err != nil {
		return err
	}
	if _, err := s.store.Member(ctx, orgID, v.ID); errors.Is(err, ErrNotFound) {
		return ErrNotMember
	} else if err != nil {
		return fmt.Errorf("finding the member: %w", err)
	}
	return wrap("leaving the organization", s.store.Leave(ctx, v.ID, orgID, v.ID, s.now()))
}

// RemoveMember drops userID from the organization; it takes org.members.manage and every permission of each role
// the member holds there, so only Owners remove Owners. The last Owner can't be removed; v leaves instead.
func (s *Service) RemoveMember(ctx context.Context, v Viewer, orgID, userID uuid.UUID) error {
	if userID == v.ID {
		return s.Leave(ctx, v, orgID)
	}
	_, held, err := s.require(ctx, v, orgID, PermMembersManage)
	if err != nil {
		return err
	}
	member, err := s.store.Member(ctx, orgID, userID)
	if err != nil {
		return wrap("finding the member", err)
	}
	for _, r := range member.Roles {
		if !auth.Covers(held, r.Permissions) {
			return fmt.Errorf("%w: removing a %s takes every permission of the role", auth.ErrForbidden, r.Name)
		}
	}
	return wrap("removing the member", s.store.Leave(ctx, v.ID, orgID, userID, s.now()))
}

// DeleteExpired drops the invitations and join requests that expired: the maintenance job's hourly task.
func (s *Service) DeleteExpired(ctx context.Context) (invitations, requests int64, err error) {
	invitations, requests, err = s.store.DeleteExpired(ctx, s.now())
	if err != nil {
		return 0, 0, fmt.Errorf("deleting expired rows: %w", err)
	}
	return invitations, requests, nil
}

// APIKeys lists the organization's API keys with their roles and secrets; it takes org.read.
func (s *Service) APIKeys(ctx context.Context, v Viewer, orgID uuid.UUID) ([]APIKey, error) {
	if _, _, err := s.require(ctx, v, orgID, PermRead); err != nil {
		return nil, err
	}
	keys, err := s.store.APIKeys(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing the API keys: %w", err)
	}
	return keys, nil
}

// apiKeyRoles finds the roles a key will hold, each an organization role the viewer, holding held, may grant: the
// grant rules, with no exception for the membership role, which is for people who join.
func (s *Service) apiKeyRoles(ctx context.Context, orgID uuid.UUID, held []string, roleIDs []uuid.UUID) ([]Role, error) {
	if !slices.Contains(held, PermGrantsManage) {
		return nil, fmt.Errorf("%w: giving a key roles takes %s", auth.ErrForbidden, PermGrantsManage)
	}
	roles := make([]Role, 0, len(roleIDs))
	for _, id := range roleIDs {
		role, err := s.organizationRole(ctx, orgID, id)
		if err != nil {
			return nil, err
		}
		if err := covers(held, role.Permissions, "giving a key "+role.Name); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, nil
}

// newSecret makes a secret that ends at expiry: the secret itself, shown once, and what the key keeps of it.
func (s *Service) newSecret(expiry Expiry) (string, Secret, error) {
	now := s.now()
	ends, err := expiry.Resolve(now)
	if err != nil {
		return "", Secret{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", Secret{}, fmt.Errorf("making the secret's ID: %w", err)
	}
	token := auth.NewSecret(auth.PrefixAPIKey)
	return token, Secret{ID: id, Hint: auth.Hint(token), CreatedAt: now, ExpiresAt: ends}, nil
}

// CreateAPIKey makes an API key of the organization holding in.RoleIDs, with its first secret, which it returns
// beside the key and never again; it takes org.principals.manage, and the roles add the grant rules.
func (s *Service) CreateAPIKey(ctx context.Context, v Viewer, orgID uuid.UUID, in APIKeyInput) (APIKey, string, error) {
	if v.Kind == auth.KindAPIKey {
		return APIKey{}, "", fmt.Errorf("%w: a key makes no keys; a person does", auth.ErrForbidden)
	}
	_, held, err := s.require(ctx, v, orgID, PermPrincipalsManage)
	if err != nil {
		return APIKey{}, "", err
	}
	if in, err = ValidateAPIKey(in); err != nil {
		return APIKey{}, "", err
	}
	roles, err := s.apiKeyRoles(ctx, orgID, held, in.RoleIDs)
	if err != nil {
		return APIKey{}, "", err
	}
	token, secret, err := s.newSecret(in.Expiry)
	if err != nil {
		return APIKey{}, "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return APIKey{}, "", fmt.Errorf("making the key's ID: %w", err)
	}
	key := APIKey{ID: id, OrgID: orgID, Name: in.Name, CreatedBy: v.ID, CreatedAt: secret.CreatedAt, Roles: roles, Secrets: []Secret{secret}}
	if err := s.store.CreateAPIKey(ctx, v.ID, key, secret, auth.Hash(token)); err != nil {
		return APIKey{}, "", wrap("recording the API key", err)
	}
	return key, token, nil
}

// apiKey finds one of the organization's keys for a viewer with org.principals.manage.
func (s *Service) apiKey(ctx context.Context, v Viewer, orgID, keyID uuid.UUID) (APIKey, []string, error) {
	_, held, err := s.require(ctx, v, orgID, PermPrincipalsManage)
	if err != nil {
		return APIKey{}, nil, err
	}
	key, err := s.store.APIKey(ctx, orgID, keyID)
	if err != nil {
		return APIKey{}, nil, wrap("finding the API key", err)
	}
	return key, held, nil
}

// RotateAPIKey gives the key a new secret ending at expiry, which it returns beside the key and never again; the
// old one works until revoked or expired. A key with two live secrets takes no third: ErrTwoSecrets. It takes
// org.principals.manage.
func (s *Service) RotateAPIKey(ctx context.Context, v Viewer, orgID, keyID uuid.UUID, expiry Expiry) (APIKey, string, error) {
	key, _, err := s.apiKey(ctx, v, orgID, keyID)
	if err != nil {
		return APIKey{}, "", err
	}
	if key.LiveSecrets(s.now()) >= 2 {
		return APIKey{}, "", ErrTwoSecrets
	}
	token, secret, err := s.newSecret(expiry)
	if err != nil {
		return APIKey{}, "", err
	}
	if err := s.store.AddSecret(ctx, v.ID, orgID, keyID, secret, auth.Hash(token), secret.CreatedAt); err != nil {
		return APIKey{}, "", wrap("adding the secret", err)
	}
	key.Secrets = append(key.Secrets, secret)
	return key, token, nil
}

// RevokeSecret ends one of the key's secrets; a key whose every secret is gone fails each call until rotated. It
// takes org.principals.manage.
func (s *Service) RevokeSecret(ctx context.Context, v Viewer, orgID, keyID, secretID uuid.UUID) error {
	if _, _, err := s.apiKey(ctx, v, orgID, keyID); err != nil {
		return err
	}
	return wrap("revoking the secret", s.store.RevokeSecret(ctx, v.ID, orgID, keyID, secretID, s.now()))
}

// DeleteAPIKey deletes the key with its secrets and grants; it takes org.principals.manage and every permission of
// the roles the key holds, as removing a member does.
func (s *Service) DeleteAPIKey(ctx context.Context, v Viewer, orgID, keyID uuid.UUID) error {
	key, held, err := s.apiKey(ctx, v, orgID, keyID)
	if err != nil {
		return err
	}
	for _, r := range key.Roles {
		if err := covers(held, r.Permissions, "deleting a key that holds "+r.Name); err != nil {
			return err
		}
	}
	return wrap("deleting the API key", s.store.DeleteAPIKey(ctx, v.ID, orgID, keyID))
}

// wrap keeps the domain's own errors as they are and gives any other what's context.
func wrap(what string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrSlugTaken), errors.Is(err, ErrInvited), errors.Is(err, ErrRequested),
		errors.Is(err, ErrAlreadyMember), errors.Is(err, ErrLastOwner), errors.Is(err, ErrRoleNameTaken),
		errors.Is(err, ErrRoleInUse), errors.Is(err, ErrGranted), errors.Is(err, ErrTwoSecrets):
		return err
	}
	return fmt.Errorf("%s: %w", what, err)
}
