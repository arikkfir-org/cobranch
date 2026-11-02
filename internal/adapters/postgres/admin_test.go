package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

func TestOrgStoreAllOrganizations(t *testing.T) {
	f := newOrgFixture(t)
	ctx := context.Background()
	globex := orgs.Organization{ID: uuid.Must(uuid.NewV7()), Slug: "globex", Name: "Globex 100%", MembershipRoleID: auth.RoleMember, CreatedAt: now}
	if err := f.s.Create(ctx, f.nia, globex, 0); err != nil {
		t.Fatal(err)
	}
	newest := globex.ID
	if f.acme.ID.String() > newest.String() {
		newest = f.acme.ID
	}
	tests := []struct {
		name      string
		page      admin.Page
		wantSlugs []string
	}{
		{name: "every organization, newest first", page: admin.Page{Limit: 10}, wantSlugs: slugsNewestFirst(f.acme, globex)},
		{name: "a slug, in any case", page: admin.Page{Query: "ACME", Limit: 10}, wantSlugs: []string{"acme"}},
		{name: "a wildcard is a character", page: admin.Page{Query: "100%", Limit: 10}, wantSlugs: []string{"globex"}},
		{name: "an underscore is a character", page: admin.Page{Query: "_", Limit: 10}, wantSlugs: nil},
		{name: "a limit", page: admin.Page{Limit: 1}, wantSlugs: slugsNewestFirst(f.acme, globex)[:1]},
		{name: "after a cursor", page: admin.Page{Limit: 10, After: newest}, wantSlugs: slugsNewestFirst(f.acme, globex)[1:]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.s.AllOrganizations(ctx, tt.page)
			if err != nil {
				t.Fatal(err)
			}
			var slugs []string
			for _, o := range got {
				slugs = append(slugs, o.Slug)
				if want := map[string]int{"acme": 2, "globex": 1}[o.Slug]; o.Members != want {
					t.Errorf("%s has %d members, want %d", o.Slug, o.Members, want)
				}
			}
			if !slices.Equal(slugs, tt.wantSlugs) {
				t.Errorf("AllOrganizations() = %v, want %v", slugs, tt.wantSlugs)
			}
		})
	}
}

// slugsNewestFirst lists the organizations' slugs by their IDs, newest first.
func slugsNewestFirst(all ...orgs.Organization) []string {
	slices.SortFunc(all, func(a, b orgs.Organization) int { return -cmpUUID(a.ID, b.ID) })
	var slugs []string
	for _, o := range all {
		slugs = append(slugs, o.Slug)
	}
	return slugs
}

func cmpUUID(a, b uuid.UUID) int {
	switch {
	case a.String() < b.String():
		return -1
	case a.String() > b.String():
		return 1
	}
	return 0
}

func TestOrgStoreAllUsers(t *testing.T) {
	f := newOrgFixture(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, "INSERT INTO grants (principal_id, role_id, scope, source) VALUES ($1, $2, 'installation', 'admins')",
		f.olivia, auth.RoleInstallationAdmin); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		query     string
		wantUsers []uuid.UUID
	}{
		{name: "by email", query: "olivia@", wantUsers: []uuid.UUID{f.olivia}},
		{name: "by email, in any case", query: "NIA@EXAMPLE", wantUsers: []uuid.UUID{f.nia}},
		{name: "nobody", query: "nobody", wantUsers: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.s.AllUsers(ctx, admin.Page{Query: tt.query, Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			var ids []uuid.UUID
			for _, u := range got {
				ids = append(ids, u.ID)
			}
			if !slices.Equal(ids, tt.wantUsers) {
				t.Errorf("AllUsers(%q) = %v, want %v", tt.query, ids, tt.wantUsers)
			}
		})
	}
	all, err := f.s.AllUsers(ctx, admin.Page{Limit: 10})
	if err != nil || len(all) != 3 {
		t.Fatalf("AllUsers() = %d users, %v; want 3", len(all), err)
	}
	for _, u := range all {
		if u.ID != f.olivia {
			continue
		}
		want := admin.User{ID: f.olivia, DisplayName: u.DisplayName, Emails: []auth.Email{{Email: "olivia@example.com", Verified: true}},
			Issuers: []string{"https://idp.example.com"}, Roles: []string{"Installation admin"}, Organizations: 1,
			CreatedAt: u.CreatedAt, LastSignInAt: u.LastSignInAt}
		if u.DisplayName == "" || u.LastSignInAt.IsZero() || !slices.Equal(u.Emails, want.Emails) || !slices.Equal(u.Issuers, want.Issuers) ||
			!slices.Equal(u.Roles, want.Roles) || u.Organizations != 1 {
			t.Errorf("Olivia = %+v, want %+v", u, want)
		}
	}
}

func TestOrgStoreCreateFor(t *testing.T) {
	f := newOrgFixture(t)
	ctx := context.Background()
	ada := f.nia // an administrator, as far as the store knows
	initech := orgs.Organization{ID: uuid.New(), Slug: "initech", Name: "Initech", MembershipRoleID: auth.RoleMember, CreatedAt: now}
	if err := f.s.CreateFor(ctx, ada, f.max, initech); err != nil {
		t.Fatal(err)
	}
	members, err := f.s.Members(ctx, initech.ID)
	if err != nil || len(members) != 1 || members[0].UserID != f.max || members[0].Roles[0].ID != auth.RoleOwner {
		t.Errorf("Members() = %+v, %v; want Max its one Owner", members, err)
	}
	var actor uuid.UUID
	var owner string
	if err := f.pool.QueryRow(ctx, "SELECT actor_id, details->>'owner' FROM events WHERE kind = 'org.created' AND org_id = $1", initech.ID).
		Scan(&actor, &owner); err != nil || actor != ada || owner != f.max.String() {
		t.Errorf("org.created by %s for %s, %v; want by the administrator for Max", actor, owner, err)
	}
	holder, err := f.s.VerifiedEmailHolder(ctx, "max@example.com")
	if err != nil || holder != f.max {
		t.Errorf("VerifiedEmailHolder() = %s, %v; want Max", holder, err)
	}
	if _, err := f.s.VerifiedEmailHolder(ctx, "nobody@example.com"); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("VerifiedEmailHolder(nobody) = %v, want orgs.ErrNotFound", err)
	}
}

// checkEntered checks that who entered Acme: the reason in its trail, Owner's permissions and, when the entry granted
// Owner (granted), the grant's source and its audit row naming who.
func checkEntered(t *testing.T, f *orgFixture, who uuid.UUID, granted bool) {
	t.Helper()
	ctx := context.Background()
	var reason string
	var orgID uuid.UUID
	if err := f.pool.QueryRow(ctx, "SELECT details->>'reason', org_id FROM events WHERE kind = 'org.entered' AND actor_id = $1", who).
		Scan(&reason, &orgID); err != nil || reason != "Support ticket 42" || orgID != f.acme.ID {
		t.Errorf("org.entered = %q in %s, %v; want the reason in Acme's trail", reason, orgID, err)
	}
	held, err := f.s.Permissions(ctx, who, auth.ScopeOrganization, f.acme.ID)
	if err != nil || !slices.Contains(held, "org.manage") {
		t.Errorf("Permissions() = %v, %v; want Owner's", held, err)
	}
	if !granted {
		return
	}
	var source string
	if err := f.pool.QueryRow(ctx, "SELECT source FROM grants WHERE principal_id = $1 AND role_id = $2 AND scope_id = $3",
		who, auth.RoleOwner, f.acme.ID).Scan(&source); err != nil || source != string(auth.SourceEntry) {
		t.Errorf("the Owner grant's source = %q, %v; want entry", source, err)
	}
	if rows := auditLog(t, f.pool, "grants"); rows[len(rows)-1].Principal == nil || *rows[len(rows)-1].Principal != who {
		t.Errorf("the grant's audit row names %v, want the administrator", rows[len(rows)-1].Principal)
	}
}

func TestOrgStoreEnter(t *testing.T) {
	tests := []struct {
		name       string
		who        func(*orgFixture) uuid.UUID
		wantEvents []string
	}{
		{name: "an administrator who isn't a member joins as Owner", who: func(f *orgFixture) uuid.UUID { return f.nia },
			wantEvents: []string{"member.joined", "org.entered"}},
		{name: "a member gains Owner", who: func(f *orgFixture) uuid.UUID { return f.max },
			wantEvents: []string{"role.granted", "org.entered"}},
		{name: "an Owner gains nothing, and the entry is recorded", who: func(f *orgFixture) uuid.UUID { return f.olivia },
			wantEvents: []string{"org.entered"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newOrgFixture(t)
			ctx := context.Background()
			who := tt.who(f)
			before := len(eventKinds(t, f.pool))
			if err := f.s.Enter(ctx, who, f.acme.ID, "Support ticket 42", now); err != nil {
				t.Fatal(err)
			}
			if got := eventKinds(t, f.pool)[before:]; !slices.Equal(got, tt.wantEvents) {
				t.Errorf("events = %v, want %v", got, tt.wantEvents)
			}
			checkEntered(t, f, who, who != f.olivia)
		})
	}
	f := newOrgFixture(t)
	if err := f.s.Enter(context.Background(), f.nia, uuid.New(), "Support", now); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Enter(an organization that isn't there) = %v, want orgs.ErrNotFound", err)
	}
}
