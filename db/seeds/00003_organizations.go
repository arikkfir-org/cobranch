package seeds

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/db/seeds/queries"
	"cobranch.dev/internal/services/auth"
)

// A demo organization, with the demo users who belong to it and the role each holds.
type demoOrganization struct {
	id         uuid.UUID
	slug, name string
	members    map[string]uuid.UUID
}

// demoOrganizations are what a reviewer sees: Acme, where the demo users hold each built-in role, and Globex, which
// Max owns, so he switches between two. Nia belongs to neither: a pending invitation to Acme waits for her, and she
// may create an organization of her own.
var demoOrganizations = []demoOrganization{
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d101"), slug: "acme", name: "Acme",
		members: map[string]uuid.UUID{"owner": auth.RoleOwner, "member": auth.RoleMember, "guest": auth.RoleGuest}},
	{id: uuid.MustParse("00000000-0000-7000-8000-00000000d102"), slug: "globex", name: "Globex",
		members: map[string]uuid.UUID{"member": auth.RoleOwner}},
}

// niaInvitation is Acme's pending invitation to Nia, as a Member, which never expires on its own.
var niaInvitation = queries.InsertSeedInvitationParams{
	ID: uuid.MustParse("00000000-0000-7000-8000-00000000d1a1"), OrgID: demoOrganizations[0].id, Email: "nia@cobranch.example",
	RoleID: auth.RoleMember, InvitedBy: uuid.NullUUID{UUID: demoUsers[0].id, Valid: true}, ExpiresAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
}

// organizations seeds the demo organizations, their members and Nia's invitation, and lets every demo user create
// organizations.
func organizations(ctx context.Context, tx *sql.Tx) error {
	q := queries.New(tx)
	users := map[string]uuid.UUID{}
	for _, u := range demoUsers {
		users[u.subject] = u.id
		if err := q.InsertSeedGrant(ctx, queries.InsertSeedGrantParams{PrincipalID: u.id, RoleID: auth.RoleInstallationUser, Scope: "installation"}); err != nil {
			return fmt.Errorf("granting %s Installation user: %w", u.subject, err)
		}
	}
	for _, o := range demoOrganizations {
		if err := seedOrganization(ctx, q, o, users); err != nil {
			return err
		}
	}
	if err := q.InsertSeedInvitation(ctx, niaInvitation); err != nil {
		return fmt.Errorf("seeding Nia's invitation: %w", err)
	}
	return nil
}

// seedOrganization seeds o with its members, each holding their role there.
func seedOrganization(ctx context.Context, q *queries.Queries, o demoOrganization, users map[string]uuid.UUID) error {
	if err := q.InsertSeedOrganization(ctx, queries.InsertSeedOrganizationParams{ID: o.id, Slug: o.slug, SlugName: o.name, MembershipRoleID: auth.RoleMember}); err != nil {
		return fmt.Errorf("seeding organization %s: %w", o.slug, err)
	}
	if err := q.InsertSeedSlug(ctx, o.slug, o.id); err != nil {
		return fmt.Errorf("seeding %s's slug: %w", o.slug, err)
	}
	for subject, role := range o.members {
		if err := q.InsertSeedMembership(ctx, o.id, users[subject]); err != nil {
			return fmt.Errorf("seeding %s's membership of %s: %w", subject, o.slug, err)
		}
		at := uuid.NullUUID{UUID: o.id, Valid: true}
		if err := q.InsertSeedGrant(ctx, queries.InsertSeedGrantParams{PrincipalID: users[subject], RoleID: role, Scope: "organization", ScopeID: at}); err != nil {
			return fmt.Errorf("granting %s a role at %s: %w", subject, o.slug, err)
		}
	}
	return nil
}
