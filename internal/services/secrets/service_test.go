package secrets_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/auth/authtest"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/secrets"
	"cobranch.dev/internal/services/secrets/secretstest"
)

// fixture is Acme, with Olivia its Owner, Max a Member who maintains website, Gwen a Guest who reads website, and Rita,
// a Member with a custom role that manages grants; Nia belongs nowhere. Acme keeps one secret, npm, and website one,
// deploy. Build is a job of website's, which Secret reader on website reaches; Lint is another, granted Secret reader
// on npm alone; Docs is a job of another organization.
type fixture struct {
	orgs                         *orgstest.Store
	store                        *secretstest.Store
	service                      *secrets.Service
	acme                         uuid.UUID
	website                      uuid.UUID
	olivia, max, gwen, rita, nia secrets.Viewer
	npm, deploy                  secrets.Secret
	build, lint, docs            uuid.UUID
}

func user(name string) secrets.Viewer {
	return secrets.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: name}}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	o := orgstest.NewStore()
	s := secretstest.NewStore(o)
	f := &fixture{orgs: o, store: s, service: secrets.NewService(s, auth.NewAuthorizer(o), authtest.Sealer{}), acme: uuid.New(), website: uuid.New()}
	f.olivia, f.max, f.gwen, f.rita, f.nia = user("Olivia"), user("Max"), user("Gwen"), user("Rita"), user("Nia")
	for _, v := range []secrets.Viewer{f.olivia, f.max, f.gwen, f.rita, f.nia} {
		o.AddUser(v.ID, v.DisplayName)
	}
	o.Orgs[f.acme] = orgs.Organization{ID: f.acme, Slug: "acme"}
	o.Repos[f.website] = f.acme
	granter := orgs.Role{ID: uuid.New(), Name: "Granter", OrgID: f.acme, Permissions: []string{"org.read", "org.grants.manage", "secrets.read"}}
	o.RoleList = append(o.RoleList, granter)
	o.AddMember(f.acme, f.olivia.ID, auth.RoleOwner)
	o.AddMember(f.acme, f.max.ID, auth.RoleMember)
	o.AddMember(f.acme, f.gwen.ID, auth.RoleGuest)
	o.AddMember(f.acme, f.rita.ID, granter.ID)
	o.Grant(f.max.ID, auth.RoleMaintainer, auth.ScopeRepository, f.website)
	o.Grant(f.gwen.ID, auth.RoleReader, auth.ScopeRepository, f.website)
	f.build, f.lint, f.docs = uuid.New(), uuid.New(), uuid.New()
	for id, name := range map[uuid.UUID]string{f.build: "build", f.lint: "lint"} {
		s.Others[id] = secretstest.Other{OrgID: f.acme, RepoID: f.website, Principal: secrets.Principal{ID: id, Kind: auth.KindJob, DisplayName: name}}
	}
	s.Others[f.docs] = secretstest.Other{OrgID: uuid.New(), Principal: secrets.Principal{ID: f.docs, Kind: auth.KindJob, DisplayName: "docs"}}
	f.npm = secrets.Secret{ID: uuid.New(), OrgID: f.acme, Name: "npm", Version: 1}
	f.deploy = secrets.Secret{ID: uuid.New(), OrgID: f.acme, RepoID: f.website, Name: "deploy", Version: 3}
	s.Add(f.npm, sealed(f.npm.ID, "npm-token"))
	s.Add(f.deploy, sealed(f.deploy.ID, "deploy-key"))
	o.Grant(f.build, auth.RoleSecretReader, auth.ScopeRepository, f.website)
	o.Grant(f.lint, auth.RoleSecretReader, auth.ScopeSecret, f.npm.ID)
	return f
}

// sealed is value as authtest.Sealer seals it for the secret id.
func sealed(id uuid.UUID, value string) []byte {
	return []byte("sealed:" + secrets.About(id) + ":" + value)
}

// manageCase is an operation of the service on the fixture, and the error it should answer.
type manageCase struct {
	name    string
	run     func(context.Context, *fixture) error
	wantErr error
}

func runManageCases(t *testing.T, tests []manageCase) {
	t.Helper()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			assertErr(t, tc.run(t.Context(), f), tc.wantErr)
		})
	}
}

func TestReadSecrets(t *testing.T) {
	runManageCases(t, []manageCase{
		{name: "an Owner lists the organization's", run: func(ctx context.Context, f *fixture) error {
			list, err := f.service.List(ctx, f.olivia, f.acme, uuid.Nil)
			if err == nil && (len(list) != 1 || list[0].Name != "npm") {
				t.Errorf("list = %+v", list)
			}
			return passed(err)
		}},
		{name: "a Maintainer lists the repository's", run: func(ctx context.Context, f *fixture) error {
			list, err := f.service.List(ctx, f.max, f.acme, f.website)
			if err == nil && (len(list) != 1 || list[0].Name != "deploy") {
				t.Errorf("list = %+v", list)
			}
			return passed(err)
		}},
		{name: "a Maintainer may not list the organization's", wantErr: auth.ErrForbidden, run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.List(ctx, f.max, f.acme, uuid.Nil)
			return passed(err)
		}},
		{name: "a Reader may not list the repository's", wantErr: auth.ErrForbidden, run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.List(ctx, f.gwen, f.acme, f.website)
			return passed(err)
		}},
		{name: "a stranger finds no organization", wantErr: secrets.ErrNotFound, run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.List(ctx, f.nia, f.acme, uuid.Nil)
			return passed(err)
		}},
		{name: "a Maintainer reads one of the repository's", run: func(ctx context.Context, f *fixture) error {
			s, err := f.service.Get(ctx, f.max, f.acme, f.deploy.ID)
			if err == nil && s.Name != "deploy" {
				t.Errorf("secret = %+v", s)
			}
			return passed(err)
		}},
		{name: "one of another organization", wantErr: secrets.ErrNotFound, run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Get(ctx, f.olivia, uuid.New(), f.npm.ID)
			return passed(err)
		}},
		{name: "a Guest who can't read the repository finds none of its", wantErr: secrets.ErrNotFound, run: func(ctx context.Context, f *fixture) error {
			f.orgs.Grants = slices.DeleteFunc(f.orgs.Grants, func(g orgstest.Grant) bool { return g.Principal == f.gwen.ID && g.Scope == auth.ScopeRepository })
			_, err := f.service.Get(ctx, f.gwen, f.acme, f.deploy.ID)
			return passed(err)
		}},
	})
}

func TestCreateSecrets(t *testing.T) {
	runManageCases(t, []manageCase{
		{name: "an Owner adds one, sealed for its row", run: func(ctx context.Context, f *fixture) error {
			s, err := f.service.Create(ctx, f.olivia, f.acme, uuid.Nil, secrets.Input{Name: "  pypi ", Value: "pypi-token"})
			if err != nil {
				return passed(err)
			}
			if s.Name != "pypi" || s.Version != 1 || s.CreatedBy != f.olivia.ID || s.RepoID != uuid.Nil {
				t.Errorf("secret = %+v", s)
			}
			if got := string(f.store.Values[s.ID]); got != string(sealed(s.ID, "pypi-token")) {
				t.Errorf("sealed = %q", got)
			}
			return nil
		}},
		{name: "a Maintainer adds one to the repository", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Create(ctx, f.max, f.acme, f.website, secrets.Input{Name: "npm", Value: "v"})
			return passed(err)
		}},
		{name: "a name taken where it lives", wantErr: secrets.ErrNameTaken, run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Create(ctx, f.max, f.acme, f.website, secrets.Input{Name: "deploy", Value: "v"})
			return passed(err)
		}},
		{name: "no value", wantErr: &orgs.ValidationError{}, run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Create(ctx, f.olivia, f.acme, uuid.Nil, secrets.Input{Name: "x"})
			return passed(err)
		}},
		{name: "a value too long", wantErr: &orgs.ValidationError{}, run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Create(ctx, f.olivia, f.acme, uuid.Nil, secrets.Input{Name: "x", Value: strings.Repeat("v", secrets.MaxValueLength+1)})
			return passed(err)
		}},
		{name: "a name with a newline", wantErr: &orgs.ValidationError{}, run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Create(ctx, f.olivia, f.acme, uuid.Nil, secrets.Input{Name: "a\nb", Value: "v"})
			return passed(err)
		}},
		{name: "an installation without a secrets key", wantErr: secrets.ErrNoSecretsKey, run: func(ctx context.Context, f *fixture) error {
			_, err := secrets.NewService(f.store, auth.NewAuthorizer(f.orgs), nil).Create(ctx, f.olivia, f.acme, uuid.Nil, secrets.Input{Name: "x", Value: "v"})
			return passed(err)
		}},
	})
}

func TestChangeSecrets(t *testing.T) {
	runManageCases(t, []manageCase{
		{name: "renaming one and keeping its value", run: func(ctx context.Context, f *fixture) error {
			s, err := f.service.Replace(ctx, f.max, f.acme, f.deploy.ID, 3, secrets.Input{Name: "deploy-key"})
			if err == nil && (s.Name != "deploy-key" || s.Version != 4 || string(f.store.Values[s.ID]) != string(sealed(s.ID, "deploy-key"))) {
				t.Errorf("secret = %+v, value %q", s, f.store.Values[s.ID])
			}
			return passed(err)
		}},
		{name: "replacing a value whatever its version", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Replace(ctx, f.olivia, f.acme, f.npm.ID, secrets.AnyVersion, secrets.Input{Name: "npm", Value: "new"})
			if err == nil && string(f.store.Values[f.npm.ID]) != string(sealed(f.npm.ID, "new")) {
				t.Errorf("value = %q", f.store.Values[f.npm.ID])
			}
			return passed(err)
		}},
		{name: "replacing one that changed", wantErr: secrets.ErrChanged, run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Replace(ctx, f.max, f.acme, f.deploy.ID, 2, secrets.Input{Name: "deploy", Value: "v"})
			return passed(err)
		}},
		{name: "a Granter may not replace one", wantErr: auth.ErrForbidden, run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Replace(ctx, f.rita, f.acme, f.npm.ID, 1, secrets.Input{Name: "npm", Value: "v"})
			return passed(err)
		}},
		{name: "deleting one takes the grants on it", run: func(ctx context.Context, f *fixture) error {
			if err := f.service.Delete(ctx, f.olivia, f.acme, f.npm.ID, 1); err != nil {
				return passed(err)
			}
			if slices.ContainsFunc(f.orgs.Grants, func(g orgstest.Grant) bool { return g.ScopeID == f.npm.ID }) {
				t.Error("a grant on the deleted secret stayed")
			}
			return nil
		}},
		{name: "deleting one that changed", wantErr: secrets.ErrChanged, run: func(ctx context.Context, f *fixture) error {
			return passed(f.service.Delete(ctx, f.olivia, f.acme, f.npm.ID, 7))
		}},
		{name: "a Maintainer may not delete the organization's", wantErr: auth.ErrForbidden, run: func(ctx context.Context, f *fixture) error {
			return passed(f.service.Delete(ctx, f.max, f.acme, f.npm.ID, 1))
		}},
	})
}

func TestGrants(t *testing.T) {
	type testCase struct {
		name    string
		run     func(context.Context, *fixture) error
		wantErr error
		want    []string // who holds a grant on npm after
	}
	tests := []testCase{
		{name: "a Granter lets a job receive one", want: []string{"build", "lint"}, run: func(ctx context.Context, f *fixture) error {
			return passed(f.service.Grant(ctx, f.rita, f.acme, f.npm.ID, f.build))
		}},
		{name: "an Owner lets a member receive one", want: []string{"Max", "lint"}, run: func(ctx context.Context, f *fixture) error {
			return passed(f.service.Grant(ctx, f.olivia, f.acme, f.npm.ID, f.max.ID))
		}},
		{name: "twice", wantErr: secrets.ErrGranted, want: []string{"lint"}, run: func(ctx context.Context, f *fixture) error {
			return passed(f.service.Grant(ctx, f.olivia, f.acme, f.npm.ID, f.lint))
		}},
		{name: "to another organization's job", wantErr: secrets.ErrNotFound, want: []string{"lint"}, run: func(ctx context.Context, f *fixture) error {
			return passed(f.service.Grant(ctx, f.olivia, f.acme, f.npm.ID, f.docs))
		}},
		{name: "a Maintainer holds no org.grants.manage", wantErr: auth.ErrForbidden, want: []string{"lint"}, run: func(ctx context.Context, f *fixture) error {
			return passed(f.service.Grant(ctx, f.max, f.acme, f.deploy.ID, f.build))
		}},
		{name: "revoking", want: nil, run: func(ctx context.Context, f *fixture) error {
			return passed(f.service.Revoke(ctx, f.rita, f.acme, f.npm.ID, f.lint))
		}},
		{name: "revoking what it doesn't hold", wantErr: secrets.ErrNotFound, want: []string{"lint"}, run: func(ctx context.Context, f *fixture) error {
			return passed(f.service.Revoke(ctx, f.olivia, f.acme, f.npm.ID, f.build))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			assertErr(t, tc.run(t.Context(), f), tc.wantErr)
			access, err := f.service.Access(t.Context(), f.olivia, f.acme, f.npm.ID)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, p := range access {
				got = append(got, p.DisplayName)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("access = %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("a Guest may not list who receives one", func(t *testing.T) {
		f := newFixture(t)
		_, err := f.service.Access(t.Context(), f.gwen, f.acme, f.deploy.ID)
		assertErr(t, err, auth.ErrForbidden)
	})
}

func TestReceive(t *testing.T) {
	type testCase struct {
		name      string
		principal func(*fixture) uuid.UUID
		ids       func(*fixture) []uuid.UUID
		sealer    auth.Sealer
		want      map[string]string
		wantErr   error
		refused   func(*fixture) *secrets.RefusedError
	}
	tests := []testCase{
		{name: "a job with Secret reader on its repository receives the repository's",
			principal: func(f *fixture) uuid.UUID { return f.build }, ids: func(f *fixture) []uuid.UUID { return []uuid.UUID{f.deploy.ID, f.deploy.ID} },
			want: map[string]string{"deploy": "deploy-key"}},
		{name: "but not the organization's",
			principal: func(f *fixture) uuid.UUID { return f.build }, ids: func(f *fixture) []uuid.UUID { return []uuid.UUID{f.deploy.ID, f.npm.ID} },
			refused: func(f *fixture) *secrets.RefusedError { return &secrets.RefusedError{ID: f.npm.ID} }},
		{name: "a job with Secret reader on one secret receives it",
			principal: func(f *fixture) uuid.UUID { return f.lint }, ids: func(f *fixture) []uuid.UUID { return []uuid.UUID{f.npm.ID} },
			want: map[string]string{"npm": "npm-token"}},
		{name: "and nothing else",
			principal: func(f *fixture) uuid.UUID { return f.lint }, ids: func(f *fixture) []uuid.UUID { return []uuid.UUID{f.deploy.ID} },
			refused: func(f *fixture) *secrets.RefusedError { return &secrets.RefusedError{ID: f.deploy.ID} }},
		{name: "an Owner's roles reach every secret of the organization",
			principal: func(f *fixture) uuid.UUID { return f.olivia.ID }, ids: func(f *fixture) []uuid.UUID { return []uuid.UUID{f.npm.ID, f.deploy.ID} },
			want: map[string]string{"npm": "npm-token", "deploy": "deploy-key"}},
		{name: "one that isn't there",
			principal: func(f *fixture) uuid.UUID { return f.olivia.ID }, ids: func(f *fixture) []uuid.UUID { return []uuid.UUID{f.build} },
			refused: func(f *fixture) *secrets.RefusedError { return &secrets.RefusedError{ID: f.build, Missing: true} }},
		{name: "none", principal: func(f *fixture) uuid.UUID { return f.docs }, ids: func(*fixture) []uuid.UUID { return nil }, want: map[string]string{}},
		{name: "without a secrets key", sealer: nil, wantErr: secrets.ErrNoSecretsKey,
			principal: func(f *fixture) uuid.UUID { return f.lint }, ids: func(f *fixture) []uuid.UUID { return []uuid.UUID{f.npm.ID} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			service := f.service
			if tc.wantErr == secrets.ErrNoSecretsKey {
				service = secrets.NewService(f.store, auth.NewAuthorizer(f.orgs), tc.sealer)
			}
			got, err := service.Receive(t.Context(), tc.principal(f), tc.ids(f))
			if tc.refused != nil {
				var refused *secrets.RefusedError
				if !errors.As(err, &refused) || *refused != *tc.refused(f) {
					t.Fatalf("err = %v, want %v", err, tc.refused(f))
				}
				return
			}
			assertErr(t, err, tc.wantErr)
			if tc.want != nil {
				assertValues(t, f, got, tc.want)
			}
		})
	}
}

// assertValues fails t unless got holds want's values, by the fixture's secrets' names.
func assertValues(t *testing.T, f *fixture, got map[uuid.UUID][]byte, want map[string]string) {
	t.Helper()
	byName := map[uuid.UUID]string{f.npm.ID: "npm", f.deploy.ID: "deploy"}
	named := map[string]string{}
	for id, v := range got {
		named[byName[id]] = string(v)
	}
	if !maps.Equal(named, want) {
		t.Errorf("values = %v, want %v", named, want)
	}
}

// passed is err, as an operation of the service answered it.
func passed(err error) error { return err }

// assertErr fails t unless err is want: nil, a sentinel error, or an error of want's type.
func assertErr(t *testing.T, err, want error) {
	t.Helper()
	var verr *orgs.ValidationError
	switch {
	case want == nil:
		if err != nil {
			t.Fatalf("err = %v", err)
		}
	case errors.As(want, &verr):
		if !errors.As(err, &verr) {
			t.Fatalf("err = %v, want a validation error", err)
		}
	case !errors.Is(err, want):
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestJobs(t *testing.T) {
	tests := []struct {
		name    string
		viewer  func(*fixture) secrets.Viewer
		repo    func(*fixture) uuid.UUID
		want    []string
		wantErr error
	}{
		{name: "a reader of the repository", viewer: func(f *fixture) secrets.Viewer { return f.gwen }, repo: func(f *fixture) uuid.UUID { return f.website }, want: []string{"build", "lint"}},
		{name: "the Owner", viewer: func(f *fixture) secrets.Viewer { return f.olivia }, repo: func(f *fixture) uuid.UUID { return f.website }, want: []string{"build", "lint"}},
		{name: "someone outside", viewer: func(f *fixture) secrets.Viewer { return f.nia }, repo: func(f *fixture) uuid.UUID { return f.website }, wantErr: secrets.ErrNotFound},
		{name: "a repository that isn't there", viewer: func(f *fixture) secrets.Viewer { return f.olivia }, repo: func(*fixture) uuid.UUID { return uuid.New() }, wantErr: secrets.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			got, err := f.service.Jobs(context.Background(), tt.viewer(f), f.acme, tt.repo(f))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Jobs() = %v, want %v", err, tt.wantErr)
			}
			var names []string
			for _, p := range got {
				names = append(names, p.DisplayName)
			}
			if !slices.Equal(names, tt.want) {
				t.Errorf("Jobs() = %v, want %v", names, tt.want)
			}
		})
	}
}
