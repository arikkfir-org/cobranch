package repos_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/repos"
	"cobranch.dev/internal/services/repos/repostest"
)

var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// fixture is Acme, with Olivia its Owner, Max a Member, Gwen a Guest and Rita a Release manager (a custom role that
// reads every repository and merges); Nia belongs nowhere. Acme has two repositories: website, which Olivia created
// and where Max is a Developer, and secret, which Olivia created and nobody else was granted. Deploy is one of Acme's
// API keys.
type fixture struct {
	orgs                         *orgstest.Store
	store                        *repostest.Store
	service                      *repos.Service
	acme                         orgs.Organization
	olivia, max, gwen, rita, nia repos.Viewer
	website, secret              repos.Repository
	deploy                       orgs.APIKey
	releaseManager, membersRole  orgs.Role
}

func user(name string) repos.Viewer {
	return repos.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: name}}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	o := orgstest.NewStore()
	s := repostest.NewStore(o)
	f := &fixture{orgs: o, store: s, service: repos.NewService(s, auth.NewAuthorizer(o))}
	f.service.SetClock(func() time.Time { return now })
	f.olivia, f.max, f.gwen, f.rita, f.nia = user("Olivia"), user("Max"), user("Gwen"), user("Rita"), user("Nia")
	for _, v := range []repos.Viewer{f.olivia, f.max, f.gwen, f.rita, f.nia} {
		o.AddUser(v.ID, v.DisplayName)
	}
	f.acme = orgs.Organization{ID: uuid.New(), Slug: "acme", Name: "Acme", MembershipRoleID: auth.RoleMember, CreatedAt: now}
	o.Orgs[f.acme.ID], o.Slugs[f.acme.Slug] = f.acme, f.acme.ID
	f.releaseManager = orgs.Role{ID: uuid.New(), Name: "Release manager", OrgID: f.acme.ID, Permissions: []string{"org.read", "repo.read", "repo.merge"}}
	f.membersRole = orgs.Role{ID: uuid.New(), Name: "Inviter", OrgID: f.acme.ID, Permissions: []string{"org.read", "org.members.manage"}}
	o.RoleList = append(o.RoleList, f.releaseManager, f.membersRole)
	o.AddMember(f.acme.ID, f.olivia.ID, auth.RoleOwner)
	o.AddMember(f.acme.ID, f.max.ID, auth.RoleMember)
	o.AddMember(f.acme.ID, f.gwen.ID, auth.RoleGuest)
	o.AddMember(f.acme.ID, f.rita.ID, f.releaseManager.ID)
	f.deploy = orgs.APIKey{ID: uuid.New(), OrgID: f.acme.ID, Name: "Deploy"}
	o.Keys[f.deploy.ID] = f.deploy
	o.Grant(f.deploy.ID, auth.RoleGuest, auth.ScopeOrganization, f.acme.ID)
	for _, r := range []*repos.Repository{&f.website, &f.secret} {
		*r = repos.Repository{ID: uuid.New(), OrgID: f.acme.ID, Name: "x", ObjectFormat: repos.ObjectFormatSHA1, CreatedBy: f.olivia.ID, CreatedAt: now}
	}
	f.website.Slug, f.secret.Slug = "website", "secret"
	for _, r := range []repos.Repository{f.website, f.secret} {
		if err := s.Create(t.Context(), f.olivia.ID, r); err != nil {
			t.Fatal(err)
		}
	}
	s.Slugs[[2]string{f.acme.ID.String(), "site"}] = f.website.ID // an old slug
	o.Grant(f.max.ID, auth.RoleDeveloper, auth.ScopeRepository, f.website.ID)
	s.Events = nil
	return f
}

// A case runs one operation and expects an error (or none) and the events it recorded.
type testCase struct {
	name       string
	run        func(ctx context.Context, f *fixture) error
	wantErr    error
	wantCode   string // a ValidationError's code
	wantEvents []string
}

// got gives an error the runner's context, as the house rules want of every error that crosses a package.
func got(err error) error {
	if err != nil {
		return fmt.Errorf("got: %w", err)
	}
	return nil
}

func runCases(t *testing.T, cases []testCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			err := tt.run(t.Context(), f)
			var verr *orgs.ValidationError
			switch {
			case tt.wantCode != "":
				if !errors.As(err, &verr) || verr.Code != tt.wantCode {
					t.Fatalf("error = %v, want validation code %s", err, tt.wantCode)
				}
			case !errors.Is(err, tt.wantErr):
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if !slices.Equal(f.store.Events, tt.wantEvents) {
				t.Errorf("events = %v, want %v", f.store.Events, tt.wantEvents)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	runCases(t, []testCase{
		{name: "a member with repos.create makes it as its Maintainer, Internal at once since Rita's role reads it", run: func(ctx context.Context, f *fixture) error {
			repo, err := f.service.Create(ctx, f.max, f.acme.ID, repos.Input{Slug: "tools", Description: " Scripts "})
			if err != nil {
				return got(err)
			}
			if repo.Name != "tools" || repo.Description != "Scripts" || repo.ObjectFormat != "sha1" ||
				repo.CreatedBy != f.max.ID || repo.Visibility != repos.VisibilityInternal || !repo.CreatedAt.Equal(now) {
				return fmt.Errorf("created %+v", repo)
			}
			_, held, err := f.service.Get(ctx, f.max, f.acme.ID, "tools")
			if err != nil || !slices.Contains(held, "repo.manage") {
				return fmt.Errorf("Max holds %v on it (%w), want Maintainer's permissions", held, err)
			}
			return nil
		}, wantEvents: []string{"repo.created"}},
		{name: "Private where no organization role reads every repository", run: func(ctx context.Context, f *fixture) error {
			f.orgs.Grants = slices.DeleteFunc(f.orgs.Grants, func(g orgstest.Grant) bool { return g.Principal == f.rita.ID })
			repo, err := f.service.Create(ctx, f.max, f.acme.ID, repos.Input{Slug: "tools"})
			if err == nil && repo.Visibility != repos.VisibilityPrivate {
				return fmt.Errorf("visibility %s", repo.Visibility)
			}
			return got(err)
		}, wantEvents: []string{"repo.created"}},
		{name: "a guest lacks repos.create", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Create(ctx, f.gwen, f.acme.ID, repos.Input{Slug: "tools"})
			return got(err)
		}, wantErr: auth.ErrForbidden},
		{name: "an outsider doesn't see the organization", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Create(ctx, f.nia, f.acme.ID, repos.Input{Slug: "tools"})
			return got(err)
		}, wantErr: repos.ErrNotFound},
		{name: "a slug another repository has", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Create(ctx, f.max, f.acme.ID, repos.Input{Slug: "website"})
			return got(err)
		}, wantErr: repos.ErrSlugTaken},
		{name: "one of the Console's pages", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Create(ctx, f.max, f.acme.ID, repos.Input{Slug: "members"})
			return got(err)
		}, wantCode: "slug.reserved"},
		{name: "a slug ending in .git", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Create(ctx, f.max, f.acme.ID, repos.Input{Slug: "tools.git"})
			return got(err)
		}, wantCode: "slug.invalid"},
	})
}

func TestValidateSlug(t *testing.T) {
	for _, tt := range []struct {
		slug string
		ok   bool
	}{
		{"a", true}, {"web-site", true}, {"web.site_2", true}, {"0day", true},
		{"", false}, {"-web", false}, {".web", false}, {"_web", false}, {"Web", false}, {"web site", false},
		{"web.git", false}, {"repos", false}, {"new", false}, {"issues", false}, {string(make([]byte, 101)), false},
	} {
		if err := repos.ValidateSlug(tt.slug); (err == nil) != tt.ok {
			t.Errorf("ValidateSlug(%q) = %v, want ok %v", tt.slug, err, tt.ok)
		}
	}
}

func TestList(t *testing.T) {
	for _, tt := range []struct {
		name    string
		viewer  func(f *fixture) repos.Viewer
		want    func(f *fixture) []repos.Repository
		wantErr error
	}{
		{name: "an Owner reads every repository", viewer: func(f *fixture) repos.Viewer { return f.olivia },
			want: func(f *fixture) []repos.Repository { return []repos.Repository{f.secret, f.website} }},
		{name: "a member reads those granted", viewer: func(f *fixture) repos.Viewer { return f.max },
			want: func(f *fixture) []repos.Repository { return []repos.Repository{f.website} }},
		{name: "a custom role with repo.read reads every one", viewer: func(f *fixture) repos.Viewer { return f.rita },
			want: func(f *fixture) []repos.Repository { return []repos.Repository{f.secret, f.website} }},
		{name: "a guest reads none", viewer: func(f *fixture) repos.Viewer { return f.gwen },
			want: func(*fixture) []repos.Repository { return nil }},
		{name: "an outsider doesn't see the organization", viewer: func(f *fixture) repos.Viewer { return f.nia }, wantErr: repos.ErrNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			list, err := f.service.List(t.Context(), tt.viewer(f), f.acme.ID)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("List() error = %v, want %v", err, tt.wantErr)
			}
			if tt.want == nil {
				return
			}
			var slugs, want []string
			for _, r := range list {
				slugs = append(slugs, r.Slug)
			}
			for _, r := range tt.want(f) {
				want = append(want, r.Slug)
			}
			if !slices.Equal(slugs, want) {
				t.Errorf("List() = %v, want %v", slugs, want)
			}
		})
	}
}

func TestVisibility(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	// Rita's custom role reads every repository, so both are Internal; without it, only the one granted to Max is.
	for slug, want := range map[string]repos.Visibility{"website": repos.VisibilityInternal, "secret": repos.VisibilityInternal} {
		if repo, _, err := f.service.Get(ctx, f.olivia, f.acme.ID, slug); err != nil || repo.Visibility != want {
			t.Errorf("%s is %s (%v), want %s", slug, repo.Visibility, err, want)
		}
	}
	f.orgs.Grants = slices.DeleteFunc(f.orgs.Grants, func(g orgstest.Grant) bool { return g.Principal == f.rita.ID })
	for slug, want := range map[string]repos.Visibility{"website": repos.VisibilityInternal, "secret": repos.VisibilityPrivate} {
		if repo, _, err := f.service.Get(ctx, f.olivia, f.acme.ID, slug); err != nil || repo.Visibility != want {
			t.Errorf("without Rita, %s is %s (%v), want %s", slug, repo.Visibility, err, want)
		}
	}
}

func TestGet(t *testing.T) {
	runCases(t, []testCase{
		{name: "by an old slug, with the viewer's permissions", run: func(ctx context.Context, f *fixture) error {
			repo, held, err := f.service.Get(ctx, f.max, f.acme.ID, "site")
			if err != nil {
				return got(err)
			}
			if repo.ID != f.website.ID || repo.Slug != "website" || !slices.Contains(held, "repo.push") || slices.Contains(held, "repo.manage") {
				return fmt.Errorf("got %s holding %v", repo.Slug, held)
			}
			return nil
		}},
		{name: "a repository the viewer doesn't read looks absent", run: func(ctx context.Context, f *fixture) error {
			_, _, err := f.service.Get(ctx, f.max, f.acme.ID, "secret")
			return got(err)
		}, wantErr: repos.ErrNotFound},
		{name: "an Owner's repository permissions", run: func(ctx context.Context, f *fixture) error {
			_, held, err := f.service.Get(ctx, f.olivia, f.acme.ID, "secret")
			if err == nil && (!slices.Equal(held, slices.Sorted(slices.Values(auth.RepositoryPermissions))) || slices.Contains(held, "org.manage")) {
				return fmt.Errorf("Olivia holds %v", held)
			}
			return got(err)
		}},
		{name: "no such slug", run: func(ctx context.Context, f *fixture) error {
			_, _, err := f.service.Get(ctx, f.olivia, f.acme.ID, "nope")
			return got(err)
		}, wantErr: repos.ErrNotFound},
		{name: "an outsider", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Access(ctx, f.nia, f.website.ID)
			return got(err)
		}, wantErr: repos.ErrNotFound},
	})
}

func TestLocate(t *testing.T) {
	// readOnly is Max's personal token that holds repo.read alone.
	readOnly := func(ctx context.Context, f *fixture) context.Context {
		return auth.WithViewer(ctx, auth.Authenticated{User: auth.User{Principal: f.max.Principal},
			Credential: auth.Credential{Kind: auth.CredentialPersonalToken, PrincipalID: f.max.ID, Permissions: []string{"repo.read"}}})
	}
	runCases(t, []testCase{
		{name: "by the organization's slug and an old one of the repository's", run: func(ctx context.Context, f *fixture) error {
			repo, held, err := f.service.Locate(ctx, f.max, "acme", "site")
			if err != nil {
				return got(err)
			}
			if repo.ID != f.website.ID || !slices.Contains(held, repos.PermPush) {
				return fmt.Errorf("got %s holding %v", repo.Slug, held)
			}
			return nil
		}},
		{name: "a token that holds repo.read alone, without org.read", run: func(ctx context.Context, f *fixture) error {
			repo, held, err := f.service.Locate(readOnly(ctx, f), f.max, "acme", "website")
			if err == nil && (repo.ID != f.website.ID || !slices.Equal(held, []string{"repo.read"})) {
				return fmt.Errorf("got %s holding %v", repo.Slug, held)
			}
			return got(err)
		}},
		{name: "a repository the viewer doesn't read looks absent", run: func(ctx context.Context, f *fixture) error {
			_, _, err := f.service.Locate(ctx, f.max, "acme", "secret")
			return got(err)
		}, wantErr: repos.ErrNotFound},
		{name: "an outsider", run: func(ctx context.Context, f *fixture) error {
			_, _, err := f.service.Locate(ctx, f.nia, "acme", "website")
			return got(err)
		}, wantErr: repos.ErrNotFound},
		{name: "no such organization", run: func(ctx context.Context, f *fixture) error {
			_, _, err := f.service.Locate(ctx, f.olivia, "globex", "website")
			return got(err)
		}, wantErr: repos.ErrNotFound},
		{name: "no such repository", run: func(ctx context.Context, f *fixture) error {
			_, _, err := f.service.Locate(ctx, f.olivia, "acme", "nope")
			return got(err)
		}, wantErr: repos.ErrNotFound},
	})
}

func TestUpdate(t *testing.T) {
	runCases(t, []testCase{
		{name: "a Maintainer renames it, keeping the old slug", run: func(ctx context.Context, f *fixture) error {
			repo, err := f.service.Update(ctx, f.olivia, f.website.ID, repos.Input{Slug: "www", Name: "WWW", Description: "The site"})
			if err != nil {
				return got(err)
			}
			for _, slug := range []string{"www", "website", "site"} {
				if r, _, err := f.service.Get(ctx, f.olivia, f.acme.ID, slug); err != nil || r.ID != repo.ID || r.Slug != "www" || r.Description != "The site" {
					return fmt.Errorf("%s found %+v (%w)", slug, r, err)
				}
			}
			return nil
		}, wantEvents: []string{"repo.updated"}},
		{name: "a Developer lacks repo.manage", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Update(ctx, f.max, f.website.ID, repos.Input{Slug: "www"})
			return got(err)
		}, wantErr: auth.ErrForbidden},
		{name: "a slug another repository has", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Update(ctx, f.olivia, f.website.ID, repos.Input{Slug: "secret"})
			return got(err)
		}, wantErr: repos.ErrSlugTaken},
		{name: "a repository the viewer doesn't read", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Update(ctx, f.max, f.secret.ID, repos.Input{Slug: "x"})
			return got(err)
		}, wantErr: repos.ErrNotFound},
	})
}

func TestAccessAndRoles(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	access, err := f.service.Access(ctx, f.max, f.website.ID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range access {
		for _, r := range a.Roles {
			names = append(names, a.Principal.DisplayName+":"+r.Name)
		}
	}
	if want := []string{"Max:Developer", "Olivia:Maintainer"}; !slices.Equal(names, want) {
		t.Errorf("Access() = %v, want %v", names, want)
	}
	roles, err := f.service.Roles(ctx, f.max, f.website.ID)
	if err != nil {
		t.Fatal(err)
	}
	names = nil
	for _, r := range roles {
		names = append(names, r.Name)
		if r.ID == f.releaseManager.ID && !slices.Equal(r.Permissions, []string{"repo.read", "repo.merge"}) {
			t.Errorf("Release manager gives %v on a repository, want its repository permissions alone", r.Permissions)
		}
	}
	if want := []string{"Reader", "Developer", "Maintainer", "Automation", "Release manager"}; !slices.Equal(names, want) {
		t.Errorf("Roles() = %v, want %v (Inviter holds no repository permission)", names, want)
	}
}

func TestGrant(t *testing.T) {
	runCases(t, []testCase{
		{name: "an Owner grants a guest Reader, who then reads it", run: func(ctx context.Context, f *fixture) error {
			if err := f.service.Grant(ctx, f.olivia, f.secret.ID, f.gwen.ID, auth.RoleReader); err != nil {
				return got(err)
			}
			list, err := f.service.List(ctx, f.gwen, f.acme.ID)
			if err != nil || len(list) != 1 || list[0].ID != f.secret.ID {
				return fmt.Errorf("Gwen lists %v (%w)", list, err)
			}
			return nil
		}, wantEvents: []string{"repo.role_granted"}},
		{name: "an Owner grants an API key Automation", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.Grant(ctx, f.olivia, f.website.ID, f.deploy.ID, auth.RoleAutomation))
		}, wantEvents: []string{"repo.role_granted"}},
		{name: "a custom role's repository permissions", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.Grant(ctx, f.olivia, f.website.ID, f.gwen.ID, f.releaseManager.ID))
		}, wantEvents: []string{"repo.role_granted"}},
		{name: "a role held already", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.Grant(ctx, f.olivia, f.website.ID, f.max.ID, auth.RoleDeveloper))
		}, wantErr: repos.ErrGranted},
		{name: "an organization role without repository permissions", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.Grant(ctx, f.olivia, f.website.ID, f.gwen.ID, f.membersRole.ID))
		}, wantCode: "role.invalid"},
		{name: "a Maintainer without org.grants.manage", run: func(ctx context.Context, f *fixture) error {
			if err := f.service.Grant(ctx, f.olivia, f.website.ID, f.max.ID, auth.RoleMaintainer); err != nil {
				return got(err)
			}
			f.store.Events = nil
			return got(f.service.Grant(ctx, f.max, f.website.ID, f.gwen.ID, auth.RoleReader))
		}, wantErr: auth.ErrForbidden},
		{name: "someone outside the organization", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.Grant(ctx, f.olivia, f.website.ID, f.nia.ID, auth.RoleReader))
		}, wantErr: repos.ErrNotFound},
	})
}

func TestRevoke(t *testing.T) {
	runCases(t, []testCase{
		{name: "an Owner revokes Max's Developer", run: func(ctx context.Context, f *fixture) error {
			if err := f.service.Revoke(ctx, f.olivia, f.website.ID, f.max.ID, auth.RoleDeveloper); err != nil {
				return got(err)
			}
			if _, _, err := f.service.Get(ctx, f.max, f.acme.ID, "website"); !errors.Is(err, repos.ErrNotFound) {
				return fmt.Errorf("Max still reads it: %w", err)
			}
			return nil
		}, wantEvents: []string{"repo.role_revoked"}},
		{name: "a role not held", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.Revoke(ctx, f.olivia, f.website.ID, f.gwen.ID, auth.RoleReader))
		}, wantErr: repos.ErrNotFound},
		{name: "a custom role that has since lost its repository permissions", run: func(ctx context.Context, f *fixture) error {
			if err := f.service.Grant(ctx, f.olivia, f.website.ID, f.gwen.ID, f.releaseManager.ID); err != nil {
				return got(err)
			}
			i := slices.IndexFunc(f.orgs.RoleList, func(r orgs.Role) bool { return r.ID == f.releaseManager.ID })
			f.orgs.RoleList[i].Permissions = []string{"org.read"}
			return got(f.service.Revoke(ctx, f.olivia, f.website.ID, f.gwen.ID, f.releaseManager.ID))
		}, wantEvents: []string{"repo.role_granted", "repo.role_revoked"}},
		{name: "a member who leaves loses their roles on its repositories", run: func(ctx context.Context, f *fixture) error {
			if err := f.orgs.Leave(ctx, f.max.ID, f.acme.ID, f.max.ID, now); err != nil {
				return got(err)
			}
			access, err := f.service.Access(ctx, f.olivia, f.website.ID)
			if err != nil || slices.ContainsFunc(access, func(a repos.Access) bool { return a.Principal.ID == f.max.ID }) {
				return fmt.Errorf("website's access after Max left: %+v (%w)", access, err)
			}
			return nil
		}},
	})
}

func TestGrantRules(t *testing.T) {
	// A custom role with org.grants.manage that reads repositories but holds nothing more grants Reader, and not
	// Developer, whose permissions it lacks.
	f := newFixture(t)
	ctx := t.Context()
	granter := orgs.Role{ID: uuid.New(), Name: "Granter", OrgID: f.acme.ID, Permissions: []string{"org.read", "org.grants.manage", "repo.read"}}
	f.orgs.RoleList = append(f.orgs.RoleList, granter)
	f.orgs.Grant(f.gwen.ID, granter.ID, auth.ScopeOrganization, f.acme.ID)
	if err := f.service.Grant(ctx, f.gwen, f.website.ID, f.rita.ID, auth.RoleReader); err != nil {
		t.Errorf("granting Reader: %v", err)
	}
	if err := f.service.Grant(ctx, f.gwen, f.website.ID, f.rita.ID, auth.RoleDeveloper); !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("granting Developer: %v, want %v", err, auth.ErrForbidden)
	}
}
