package admin_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
)

var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// fixture is an installation with Ada, its administrator; Uma, who may create organizations; and Olivia, Owner of
// Acme, where Max is a Member. Globex has no member Ada knows.
type fixture struct {
	store                 *orgstest.Store
	service               *admin.Service
	acme, globex          orgs.Organization
	ada, uma, olivia, max auth.User
}

func user(name, email string) auth.User {
	return auth.User{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: name}, Emails: []auth.Email{{Email: email, Verified: true}}}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	s := orgstest.NewStore()
	f := &fixture{store: s, service: admin.NewService(s, auth.NewAuthorizer(s), auth.Settings{})}
	f.service.SetClock(func() time.Time { return now })
	f.ada, f.uma, f.olivia, f.max = user("Ada Admin", "ada@example.com"), user("Uma User", "uma@example.com"),
		user("Olivia", "olivia@example.com"), user("Max", "max@example.com")
	for _, u := range []auth.User{f.ada, f.uma, f.olivia, f.max} {
		s.AddUser(u.ID, u.DisplayName, u.Emails[0].Email)
	}
	s.Grant(f.ada.ID, auth.RoleInstallationAdmin, auth.ScopeInstallation, uuid.Nil)
	s.Grant(f.uma.ID, auth.RoleInstallationUser, auth.ScopeInstallation, uuid.Nil)
	f.acme = orgs.Organization{ID: uuid.MustParse("00000000-0000-7000-8000-0000000000a1"), Slug: "acme", Name: "Acme", MembershipRoleID: auth.RoleMember, CreatedAt: now}
	f.globex = orgs.Organization{ID: uuid.MustParse("00000000-0000-7000-8000-0000000000a2"), Slug: "globex", Name: "Globex", MembershipRoleID: auth.RoleMember, CreatedAt: now}
	for _, o := range []orgs.Organization{f.acme, f.globex} {
		s.Orgs[o.ID], s.Slugs[o.Slug] = o, o.ID
	}
	s.AddMember(f.acme.ID, f.olivia.ID, auth.RoleOwner)
	s.AddMember(f.acme.ID, f.max.ID, auth.RoleMember)
	return f
}

func TestPermissions(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		name string
		v    auth.User
		want []string
	}{
		{name: "an administrator", v: f.ada, want: auth.InstallationPermissions},
		{name: "an installation user", v: f.uma, want: []string{"orgs.create"}},
		{name: "a user with no installation role", v: f.max, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.service.Permissions(context.Background(), tt.v)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, slices.Sorted(slices.Values(tt.want))) {
				t.Errorf("Permissions() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOrganizations(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		name      string
		v         auth.User
		page      admin.Page
		wantSlugs []string
		wantNext  bool
		wantErr   error
	}{
		{name: "every organization, newest first, with its members", v: f.ada, wantSlugs: []string{"globex", "acme"}},
		{name: "a search by name, in any case", v: f.ada, page: admin.Page{Query: " ACM "}, wantSlugs: []string{"acme"}},
		{name: "a first page", v: f.ada, page: admin.Page{Limit: 1}, wantSlugs: []string{"globex"}, wantNext: true},
		{name: "the page after it", v: f.ada, page: admin.Page{Limit: 1, After: f.globex.ID}, wantSlugs: []string{"acme"}},
		{name: "a limit past the most", v: f.ada, page: admin.Page{Limit: 1000}, wantSlugs: []string{"globex", "acme"}},
		{name: "not without installation.manage", v: f.uma, wantErr: auth.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.service.Organizations(context.Background(), tt.v, tt.page)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Organizations() error = %v, want %v", err, tt.wantErr)
			}
			var slugs []string
			for _, o := range got.Items {
				slugs = append(slugs, o.Slug)
				if o.Slug == "acme" && o.Members != 2 {
					t.Errorf("Acme has %d members, want 2", o.Members)
				}
			}
			if !slices.Equal(slugs, tt.wantSlugs) || (got.Next != uuid.Nil) != tt.wantNext {
				t.Errorf("Organizations() = %v, next %v; want %v, a next page %v", slugs, got.Next, tt.wantSlugs, tt.wantNext)
			}
		})
	}
}

func TestUsers(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		name      string
		v         auth.User
		page      admin.Page
		wantNames []string
		wantErr   error
	}{
		{name: "a search by email", v: f.ada, page: admin.Page{Query: "olivia@"}, wantNames: []string{"Olivia"}},
		{name: "a search by name", v: f.ada, page: admin.Page{Query: "admin"}, wantNames: []string{"Ada Admin"}},
		{name: "not without installation.users.manage", v: f.uma, wantErr: auth.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.service.Users(context.Background(), tt.v, tt.page)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Users() error = %v, want %v", err, tt.wantErr)
			}
			var names []string
			for _, u := range got.Items {
				names = append(names, u.DisplayName)
			}
			if !slices.Equal(names, tt.wantNames) {
				t.Errorf("Users() = %v, want %v", names, tt.wantNames)
			}
		})
	}
	all, err := f.service.Users(context.Background(), f.ada, admin.Page{Query: "Ada"})
	if err != nil || len(all.Items) != 1 || !slices.Equal(all.Items[0].Roles, []string{"Installation admin"}) {
		t.Errorf("Users(Ada) = %+v, %v; want her with Installation admin", all, err)
	}
}

// failed checks op's error against the one wanted, wantErr or a validation error of wantCode, and reports whether op
// failed, so there is nothing more to check.
func failed(t *testing.T, op string, err, wantErr error, wantCode string) bool {
	t.Helper()
	if wantCode != "" {
		var invalid *orgs.ValidationError
		if !errors.As(err, &invalid) || invalid.Code != wantCode {
			t.Fatalf("%s() error = %v, want code %s", op, err, wantCode)
		}
		return true
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("%s() error = %v, want %v", op, err, wantErr)
	}
	return err != nil
}

func TestCreateOrganization(t *testing.T) {
	tests := []struct {
		name, slug, orgName, email string
		v                          func(*fixture) auth.User
		wantErr                    error
		wantCode                   string
	}{
		{name: "for a user, who becomes its Owner", slug: "initech", orgName: "Initech", email: " Max@Example.com "},
		{name: "an invalid slug", slug: "-no", orgName: "No", email: "max@example.com", wantCode: "slug.invalid"},
		{name: "a slug another organization has", slug: "acme", orgName: "Acme", email: "max@example.com", wantErr: orgs.ErrSlugTaken},
		{name: "an email nobody holds", slug: "initech", orgName: "Initech", email: "nobody@example.com", wantCode: "owner.not_found"},
		{name: "not an email", slug: "initech", orgName: "Initech", email: "max", wantCode: "email.invalid"},
		{name: "not without installation.users.manage", slug: "initech", orgName: "Initech", email: "max@example.com",
			v: func(f *fixture) auth.User { return f.uma }, wantErr: auth.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			v := f.ada
			if tt.v != nil {
				v = tt.v(f)
			}
			got, err := f.service.CreateOrganization(context.Background(), v, tt.slug, tt.orgName, tt.email)
			if failed(t, "CreateOrganization", err, tt.wantErr, tt.wantCode) {
				return
			}
			members := f.store.Memberships[got.ID]
			if got.Slug != tt.slug || got.Members != 1 || !slices.Equal(members, []uuid.UUID{f.max.ID}) {
				t.Errorf("CreateOrganization() = %+v with members %v, want Max its one member", got, members)
			}
			held, _ := f.store.Permissions(context.Background(), f.max.ID, auth.ScopeOrganization, got.ID)
			if !slices.Contains(held, "org.manage") {
				t.Errorf("Max holds %v at the new organization, want Owner's", held)
			}
		})
	}
}

func TestEnter(t *testing.T) {
	tests := []struct {
		name, slug, reason string
		v                  func(*fixture) auth.User
		setup              func(*fixture)
		wantErr            error
		wantCode           string
		wantEvents         []string
	}{
		{name: "an organization the administrator isn't in", slug: "acme", reason: "Support ticket 42",
			wantEvents: []string{"member.joined", "org.entered"}},
		{name: "by an old slug", slug: "acme-corp", reason: "Support ticket 42",
			setup: func(f *fixture) { f.store.Slugs["acme-corp"] = f.acme.ID }, wantEvents: []string{"member.joined", "org.entered"}},
		{name: "as a member already", slug: "acme", reason: "Support ticket 42",
			setup:      func(f *fixture) { f.store.AddMember(f.acme.ID, f.ada.ID, auth.RoleGuest) },
			wantEvents: []string{"org.entered"}},
		{name: "no reason", slug: "acme", reason: "  ", wantCode: "reason.invalid"},
		{name: "too long a reason", slug: "acme", reason: strings.Repeat("é", admin.MaxReason+1), wantCode: "reason.invalid"},
		{name: "an organization that isn't there", slug: "nope", reason: "Support", wantErr: orgs.ErrNotFound},
		{name: "not without installation.manage", slug: "acme", reason: "Support",
			v: func(f *fixture) auth.User { return f.olivia }, wantErr: auth.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			if tt.setup != nil {
				tt.setup(f)
			}
			f.store.Events = nil
			v := f.ada
			if tt.v != nil {
				v = tt.v(f)
			}
			got, err := f.service.Enter(context.Background(), v, tt.slug, tt.reason)
			if failed(t, "Enter", err, tt.wantErr, tt.wantCode) {
				return
			}
			if got.ID != f.acme.ID || !slices.Equal(f.store.Events, tt.wantEvents) {
				t.Errorf("Enter() = %s with events %v, want Acme with %v", got.Slug, f.store.Events, tt.wantEvents)
			}
			if want := []orgstest.Entry{{Org: f.acme.ID, User: f.ada.ID, Reason: strings.TrimSpace(tt.reason)}}; !slices.Equal(f.store.Entered, want) {
				t.Errorf("entries = %v, want %v", f.store.Entered, want)
			}
			held, _ := f.store.Permissions(context.Background(), f.ada.ID, auth.ScopeOrganization, f.acme.ID)
			if !slices.Contains(held, "org.manage") {
				t.Errorf("Ada holds %v at Acme, want Owner's", held)
			}
		})
	}
}
