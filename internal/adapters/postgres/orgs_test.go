package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// orgFixture is Acme on a migrated database: Olivia created it, Max joined as a Member, and Nia, who belongs
// nowhere, holds nia@example.com.
type orgFixture struct {
	s                *OrgStore
	pool             *pgxpool.Pool
	acme             orgs.Organization
	olivia, max, nia uuid.UUID
}

func newOrgFixture(t *testing.T) *orgFixture {
	t.Helper()
	auths, pool := authStore(t)
	f := &orgFixture{s: NewOrgStore(pool), pool: pool}
	ctx := context.Background()
	for _, u := range []struct {
		id      *uuid.UUID
		subject string
		email   string
	}{{&f.olivia, "1", "olivia@example.com"}, {&f.max, "2", "max@example.com"}, {&f.nia, "3", "nia@example.com"}} {
		up := signUp("https://idp.example.com", u.subject, u.email, true)
		if err := auths.SignUp(ctx, up); err != nil {
			t.Fatal(err)
		}
		*u.id = up.Principal.ID
	}
	f.acme = orgs.Organization{ID: uuid.New(), Slug: "acme", Name: "Acme", MembershipRoleID: auth.RoleMember, AcceptsJoinRequests: true, CreatedAt: now}
	if err := f.s.Create(ctx, f.olivia, f.acme); err != nil {
		t.Fatal(err)
	}
	req := orgs.JoinRequest{ID: uuid.New(), OrgID: f.acme.ID, UserID: f.max, Note: "hi", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := f.s.Request(ctx, f.max, req); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Approve(ctx, f.olivia, req, auth.RoleMember, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestOrgStoreCreate(t *testing.T) {
	f := newOrgFixture(t)
	ctx := context.Background()
	org, err := f.s.BySlug(ctx, "acme")
	if err != nil || org.ID != f.acme.ID || org.Name != "Acme" || org.Providers != nil {
		t.Fatalf("BySlug() = %+v, %v", org, err)
	}
	held, err := f.s.Permissions(ctx, f.olivia, auth.ScopeOrganization, f.acme.ID)
	if err != nil || !slices.Contains(held, "org.manage") || !slices.Contains(held, "repo.push") {
		t.Errorf("Olivia holds %v (%v), want the Owner's permissions", held, err)
	}
	if held, _ := f.s.Permissions(ctx, f.olivia, auth.ScopeInstallation, uuid.Nil); len(held) != 0 {
		t.Errorf("Olivia holds %v at the installation, want nothing", held)
	}
	want := []string{"user.signed_up", "user.signed_up", "user.signed_up", "org.created", "member.joined", "join_request.made", "join_request.approved", "member.joined"}
	if got := eventKinds(t, f.pool); !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE org_id = $1", f.acme.ID); n != 5 {
		t.Errorf("%d events carry Acme's id, want 5", n)
	}
	// The organization, its slug, Olivia's own membership and Max's, which Olivia approved.
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name IN ('organizations', 'organization_slugs', 'memberships') AND principal_id = $1", f.olivia); n != 4 {
		t.Errorf("%d audit rows name Olivia for creating Acme and approving Max, want 4", n)
	}
	err = f.s.Create(ctx, f.nia, orgs.Organization{ID: uuid.New(), Slug: "acme", Name: "Another", MembershipRoleID: auth.RoleMember, CreatedAt: now})
	if !errors.Is(err, orgs.ErrSlugTaken) {
		t.Errorf("Create() with a taken slug error = %v, want slug taken", err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM organizations"); n != 1 {
		t.Errorf("%d organizations after the refused one, want 1", n)
	}
}

func TestOrgStoreUpdate(t *testing.T) {
	f := newOrgFixture(t)
	ctx := context.Background()
	org := f.acme
	org.Slug, org.Name, org.MembershipRoleID, org.AcceptsJoinRequests, org.Providers = "acme-inc", "Acme Inc", auth.RoleGuest, false, []string{"google"}
	if err := f.s.Update(ctx, f.olivia, org); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"acme", "acme-inc"} {
		got, err := f.s.BySlug(ctx, slug)
		if err != nil || got.Slug != "acme-inc" || got.Name != "Acme Inc" || got.MembershipRoleID != auth.RoleGuest || got.AcceptsJoinRequests || !slices.Equal(got.Providers, []string{"google"}) {
			t.Errorf("BySlug(%s) = %+v, %v", slug, got, err)
		}
	}
	other := orgs.Organization{ID: uuid.New(), Slug: "globex", Name: "Globex", MembershipRoleID: auth.RoleMember, CreatedAt: now}
	if err := f.s.Create(ctx, f.nia, other); err != nil {
		t.Fatal(err)
	}
	other.Slug = "acme"
	if err := f.s.Update(ctx, f.nia, other); !errors.Is(err, orgs.ErrSlugTaken) {
		t.Errorf("Update() to an old slug error = %v, want slug taken", err)
	}
	if _, err := f.s.ByID(ctx, uuid.New()); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("ByID() of nothing error = %v, want not found", err)
	}
}

func TestOrgStoreMembers(t *testing.T) {
	f := newOrgFixture(t)
	ctx := context.Background()
	members, err := f.s.Members(ctx, f.acme.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("Members() = %+v, %v", members, err)
	}
	var names []string
	for _, m := range members {
		for _, r := range m.Roles {
			names = append(names, r.Name)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"Member", "Owner"}) {
		t.Errorf("roles held = %v, want a Member and an Owner", names)
	}
	m, err := f.s.Member(ctx, f.acme.ID, f.max)
	if err != nil || len(m.Roles) != 1 || m.Roles[0].ID != auth.RoleMember || m.JoinedAt.IsZero() {
		t.Errorf("Member(max) = %+v, %v", m, err)
	}
	if id, err := f.s.MemberByEmail(ctx, f.acme.ID, "max@example.com"); err != nil || id != f.max {
		t.Errorf("MemberByEmail() = %s, %v", id, err)
	}
	if _, err := f.s.MemberByEmail(ctx, f.acme.ID, "nia@example.com"); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("MemberByEmail(nia) error = %v, want not found", err)
	}
	list, err := f.s.OrganizationsOf(ctx, f.max)
	if err != nil || len(list) != 1 || list[0].Slug != "acme" {
		t.Errorf("OrganizationsOf(max) = %+v, %v", list, err)
	}
	if n, err := f.s.CountRoleHolders(ctx, f.acme.ID, auth.RoleOwner); err != nil || n != 1 {
		t.Errorf("CountRoleHolders(Owner) = %d, %v", n, err)
	}
	if err := f.s.Leave(ctx, f.olivia, f.acme.ID, f.max, now); err != nil {
		t.Fatal(err)
	}
	if held, _ := f.s.Permissions(ctx, f.max, auth.ScopeOrganization, f.acme.ID); len(held) != 0 {
		t.Errorf("Max holds %v after removal", held)
	}
	if err := f.s.Leave(ctx, f.olivia, f.acme.ID, f.max, now); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Leave() twice error = %v, want not found", err)
	}
	if kinds := eventKinds(t, f.pool); kinds[len(kinds)-1] != "member.removed" {
		t.Errorf("last event = %s, want member.removed", kinds[len(kinds)-1])
	}
}

func TestOrgStoreInvitations(t *testing.T) {
	f := newOrgFixture(t)
	ctx := context.Background()
	inv := orgs.Invitation{ID: uuid.New(), OrgID: f.acme.ID, Email: "nia@example.com", RoleID: auth.RoleGuest, InvitedBy: f.olivia, State: orgs.StatePending, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := f.s.Invite(ctx, f.olivia, inv); err != nil {
		t.Fatal(err)
	}
	again := inv
	again.ID = uuid.New()
	if err := f.s.Invite(ctx, f.olivia, again); !errors.Is(err, orgs.ErrInvited) {
		t.Errorf("Invite() twice error = %v, want invited", err)
	}
	got, err := f.s.Invitation(ctx, inv.ID)
	if err != nil || got.OrgSlug != "acme" || got.OrgName != "Acme" || got.Email != inv.Email || got.State != orgs.StatePending {
		t.Fatalf("Invitation() = %+v, %v", got, err)
	}
	for name, list := range map[string]func() ([]orgs.Invitation, error){
		"Invitations": func() ([]orgs.Invitation, error) { return f.s.Invitations(ctx, f.acme.ID, now) },
		"InvitationsFor": func() ([]orgs.Invitation, error) {
			return f.s.InvitationsFor(ctx, []string{"x@example.com", "nia@example.com"}, now)
		},
		"after it expired": func() ([]orgs.Invitation, error) { return f.s.Invitations(ctx, f.acme.ID, now.Add(2*time.Hour)) },
	} {
		got, err := list()
		want := 1
		if name == "after it expired" {
			want = 0
		}
		if err != nil || len(got) != want {
			t.Errorf("%s = %d (%v), want %d", name, len(got), err, want)
		}
	}
	if err := f.s.Accept(ctx, f.nia, got, f.nia, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	m, err := f.s.Member(ctx, f.acme.ID, f.nia)
	if err != nil || len(m.Roles) != 1 || m.Roles[0].ID != auth.RoleGuest {
		t.Errorf("Member(nia) = %+v, %v, want a Guest", m, err)
	}
	if err := f.s.CloseInvitation(ctx, f.nia, inv.ID, orgs.StateDeclined, now); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("CloseInvitation() of an accepted one error = %v, want not found", err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM invitations WHERE state = 'accepted' AND decided_by = $1", f.nia); n != 1 {
		t.Errorf("%d accepted invitations decided by Nia, want 1", n)
	}
}

func TestOrgStoreJoinRequests(t *testing.T) {
	f := newOrgFixture(t)
	ctx := context.Background()
	req := orgs.JoinRequest{ID: uuid.New(), OrgID: f.acme.ID, UserID: f.nia, Note: "Let me in", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := f.s.Request(ctx, f.nia, req); err != nil {
		t.Fatal(err)
	}
	again := req
	again.ID = uuid.New()
	if err := f.s.Request(ctx, f.nia, again); !errors.Is(err, orgs.ErrRequested) {
		t.Errorf("Request() twice error = %v, want requested", err)
	}
	got, err := f.s.JoinRequest(ctx, req.ID)
	if err != nil || got.OrgSlug != "acme" || got.UserName != "Alice" || got.Note != "Let me in" {
		t.Fatalf("JoinRequest() = %+v, %v", got, err)
	}
	if list, err := f.s.JoinRequests(ctx, f.acme.ID, now); err != nil || len(list) != 1 {
		t.Errorf("JoinRequests() = %d, %v", len(list), err)
	}
	if list, err := f.s.JoinRequestsOf(ctx, f.nia, now.Add(2*time.Hour)); err != nil || len(list) != 0 {
		t.Errorf("JoinRequestsOf() after expiry = %d, %v", len(list), err)
	}
	if err := f.s.CloseJoinRequest(ctx, f.olivia, req.ID, orgs.StateDeclined, now); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Approve(ctx, f.olivia, got, auth.RoleMember, now); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Approve() of a declined request error = %v, want not found", err)
	}
	if _, err := f.s.Member(ctx, f.acme.ID, f.nia); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Nia is a member after a refused approval: %v", err)
	}
}

func TestOrgStoreRolesAndExpiry(t *testing.T) {
	f := newOrgFixture(t)
	ctx := context.Background()
	roles, err := f.s.Roles(ctx)
	if err != nil || len(roles) != 3 || roles[0].Name != "Guest" || !roles[0].BuiltIn {
		t.Errorf("Roles() = %+v, %v, want Guest, Member and Owner", roles, err)
	}
	if r, err := f.s.Role(ctx, auth.RoleOwner); err != nil || r.Name != "Owner" || !slices.Contains(r.Permissions, "org.manage") {
		t.Errorf("Role(Owner) = %+v, %v", r, err)
	}
	if _, err := f.s.Role(ctx, uuid.New()); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Role() of nothing error = %v", err)
	}
	for i, expires := range []time.Time{now.Add(-time.Hour), now.Add(time.Hour)} {
		inv := orgs.Invitation{ID: uuid.New(), OrgID: f.acme.ID, Email: string(rune('a'+i)) + "@example.com", RoleID: auth.RoleGuest, InvitedBy: f.olivia, CreatedAt: now, ExpiresAt: expires}
		if err := f.s.Invite(ctx, f.olivia, inv); err != nil {
			t.Fatal(err)
		}
	}
	req := orgs.JoinRequest{ID: uuid.New(), OrgID: f.acme.ID, UserID: f.nia, CreatedAt: now, ExpiresAt: now.Add(-time.Minute)}
	if err := f.s.Request(ctx, f.nia, req); err != nil {
		t.Fatal(err)
	}
	invitations, requests, err := f.s.DeleteExpired(ctx, now)
	if err != nil || invitations != 1 || requests != 1 {
		t.Errorf("DeleteExpired() = %d, %d, %v, want 1 and 1", invitations, requests, err)
	}
}
