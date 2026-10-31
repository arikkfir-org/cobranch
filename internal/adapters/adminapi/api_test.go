package adminapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// fixture is an installation with Ada, its administrator, and Olivia, Owner of Acme and Globex, where Max is a Member
// of Acme: over the administration's service on a fake store.
type fixture struct {
	store            *orgstest.Store
	ada, olivia, max auth.Authenticated
	acme, globex     orgs.Organization
	handler          http.Handler
}

func viewerOf(name, email string) auth.Authenticated {
	return auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: name},
		Emails: []auth.Email{{Email: email, Verified: true}}}}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWith(t, auth.Settings{})
}

// newFixtureWith is the fixture under the values' sign-up settings.
func newFixtureWith(t *testing.T, values auth.Settings) *fixture {
	t.Helper()
	s := orgstest.NewStore()
	service := admin.NewService(s, auth.NewAuthorizer(s), values)
	service.SetClock(func() time.Time { return now })
	f := &fixture{store: s, handler: NewHandler("v1", service)}
	f.ada, f.olivia, f.max = viewerOf("Ada Admin", "ada@example.com"), viewerOf("Olivia", "olivia@example.com"), viewerOf("Max", "max@example.com")
	for _, v := range []auth.Authenticated{f.ada, f.olivia, f.max} {
		s.AddUser(v.User.ID, v.User.DisplayName, v.User.Emails[0].Email)
	}
	s.Grant(f.ada.User.ID, auth.RoleInstallationAdmin, auth.ScopeInstallation, uuid.Nil)
	f.acme = orgs.Organization{ID: uuid.MustParse("00000000-0000-7000-8000-0000000000a1"), Slug: "acme", Name: "Acme", MembershipRoleID: auth.RoleMember, CreatedAt: now}
	f.globex = orgs.Organization{ID: uuid.MustParse("00000000-0000-7000-8000-0000000000a2"), Slug: "globex", Name: "Globex", MembershipRoleID: auth.RoleMember, CreatedAt: now}
	for _, o := range []orgs.Organization{f.acme, f.globex} {
		s.Orgs[o.ID], s.Slugs[o.Slug] = o, o.ID
		s.AddMember(o.ID, f.olivia.User.ID, auth.RoleOwner)
	}
	s.AddMember(f.acme.ID, f.max.User.ID, auth.RoleMember)
	return f
}

// call serves one request to the API as viewer (nil: Anonymous) and returns the recorder.
func (f *fixture) call(viewer *auth.Authenticated, method, path, body string) *httptest.ResponseRecorder {
	return f.callWith(viewer, method, path, body, nil)
}

// callWith is call with the request's headers set to header's.
func (f *fixture) callWith(viewer *auth.Authenticated, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	if viewer != nil {
		req = req.WithContext(auth.WithViewer(req.Context(), *viewer))
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func ada(f *fixture) *auth.Authenticated    { return &f.ada }
func olivia(f *fixture) *auth.Authenticated { return &f.olivia }

func decode[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("body %s is not JSON: %v", body, err)
	}
	return v
}

// cursor is the cursor that names the page after id.
func cursor(id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString(id[:])
}

type apiCase struct {
	name       string
	viewer     func(*fixture) *auth.Authenticated
	method     string
	path       func(*fixture) string
	body       string
	wantStatus int
	wantCode   string
	check      func(t *testing.T, f *fixture, rec *httptest.ResponseRecorder)
}

func fixed(p string) func(*fixture) string { return func(*fixture) string { return p } }

func runCases(t *testing.T, cases []apiCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			var viewer *auth.Authenticated
			if tt.viewer != nil {
				viewer = tt.viewer(f)
			}
			rec := f.call(viewer, tt.method, tt.path(f), tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantCode != "" {
				p := decode[Problem](t, rec.Body.Bytes())
				if p.Code != tt.wantCode || p.Status != rec.Code || rec.Header().Get("Content-Type") != "application/problem+json" {
					t.Errorf("problem = %+v as %s, want code %s", p, rec.Header().Get("Content-Type"), tt.wantCode)
				}
			}
			if tt.check != nil {
				tt.check(t, f, rec)
			}
		})
	}
}

func TestVersionAndMe(t *testing.T) {
	runCases(t, []apiCase{
		{name: "the version, to anyone", method: "GET", path: fixed("/v1/version"), wantStatus: 200,
			check: func(t *testing.T, _ *fixture, rec *httptest.ResponseRecorder) {
				if got := decode[GetVersion200JSONResponse](t, rec.Body.Bytes()); got.Version != "v1" {
					t.Errorf("version = %q", got.Version)
				}
			}},
		{name: "an administrator", viewer: ada, method: "GET", path: fixed("/v1/me"), wantStatus: 200,
			check: func(t *testing.T, f *fixture, rec *httptest.ResponseRecorder) {
				me := decode[Me](t, rec.Body.Bytes())
				if me.Id != f.ada.User.ID || !slices.Contains(me.Permissions, admin.PermManage) || len(me.Emails) != 1 {
					t.Errorf("me = %+v", me)
				}
			}},
		{name: "a user who holds nothing at the installation", viewer: olivia, method: "GET", path: fixed("/v1/me"), wantStatus: 200,
			check: func(t *testing.T, _ *fixture, rec *httptest.ResponseRecorder) {
				if !strings.Contains(rec.Body.String(), `"permissions":[]`) {
					t.Errorf("me = %s, want no permissions, as []", rec.Body)
				}
			}},
		{name: "Anonymous", method: "GET", path: fixed("/v1/me"), wantStatus: 401, wantCode: "auth.unauthenticated"},
		{name: "an unknown path", viewer: ada, method: "GET", path: fixed("/v1/nope"), wantStatus: 404, wantCode: "route.not_found"},
	})
}

func TestListOrganizations(t *testing.T) {
	slugs := func(rec *httptest.ResponseRecorder, t *testing.T) []string {
		body := decode[struct {
			Organizations []Organization `json:"organizations"`
		}](t, rec.Body.Bytes())
		var out []string
		for _, o := range body.Organizations {
			out = append(out, o.Slug)
		}
		return out
	}
	runCases(t, []apiCase{
		{name: "every organization, newest first, with its members", viewer: ada, method: "GET", path: fixed("/v1/orgs"), wantStatus: 200,
			check: func(t *testing.T, _ *fixture, rec *httptest.ResponseRecorder) {
				if got := slugs(rec, t); !slices.Equal(got, []string{"globex", "acme"}) || rec.Header().Get("Link") != "" {
					t.Errorf("organizations = %v with Link %q, want globex and acme, no next page", got, rec.Header().Get("Link"))
				}
				if !strings.Contains(rec.Body.String(), `"members":2`) {
					t.Errorf("body = %s, want Acme's two members", rec.Body)
				}
			}},
		{name: "a first page names the next", viewer: ada, method: "GET", path: fixed("/v1/orgs?limit=1&q=e"), wantStatus: 200,
			check: func(t *testing.T, f *fixture, rec *httptest.ResponseRecorder) {
				want := `<?cursor=` + cursor(f.globex.ID) + `&limit=1&q=e>; rel="next"`
				if got := slugs(rec, t); !slices.Equal(got, []string{"globex"}) || rec.Header().Get("Link") != want {
					t.Errorf("organizations = %v with Link %q, want globex and %q", got, rec.Header().Get("Link"), want)
				}
			}},
		{name: "the page a cursor names", viewer: ada, method: "GET", path: func(f *fixture) string { return "/v1/orgs?limit=1&cursor=" + cursor(f.globex.ID) }, wantStatus: 200,
			check: func(t *testing.T, _ *fixture, rec *httptest.ResponseRecorder) {
				if got := slugs(rec, t); !slices.Equal(got, []string{"acme"}) || rec.Header().Get("Link") != "" {
					t.Errorf("organizations = %v with Link %q, want acme alone", got, rec.Header().Get("Link"))
				}
			}},
		{name: "a search", viewer: ada, method: "GET", path: fixed("/v1/orgs?q=ACM"), wantStatus: 200,
			check: func(t *testing.T, _ *fixture, rec *httptest.ResponseRecorder) {
				if got := slugs(rec, t); !slices.Equal(got, []string{"acme"}) {
					t.Errorf("organizations = %v, want acme", got)
				}
			}},
		{name: "a cursor that isn't one", viewer: ada, method: "GET", path: fixed("/v1/orgs?cursor=nope"), wantStatus: 400, wantCode: "cursor.invalid"},
		{name: "a limit that isn't a number", viewer: ada, method: "GET", path: fixed("/v1/orgs?limit=many"), wantStatus: 400, wantCode: "request.invalid"},
		{name: "not without installation.manage", viewer: olivia, method: "GET", path: fixed("/v1/orgs"), wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "not for Anonymous", method: "GET", path: fixed("/v1/orgs"), wantStatus: 401, wantCode: "auth.unauthenticated"},
	})
}

func TestCreateOrganization(t *testing.T) {
	runCases(t, []apiCase{
		{name: "for a user", viewer: ada, method: "POST", path: fixed("/v1/orgs"), body: `{"slug":"initech","name":"Initech","ownerEmail":"max@example.com"}`, wantStatus: 201,
			check: func(t *testing.T, f *fixture, rec *httptest.ResponseRecorder) {
				got := decode[Organization](t, rec.Body.Bytes())
				if got.Slug != "initech" || got.Members != 1 || !slices.Equal(f.store.Memberships[got.Id], []uuid.UUID{f.max.User.ID}) {
					t.Errorf("created %+v with members %v, want Max its Owner", got, f.store.Memberships[got.Id])
				}
			}},
		{name: "a slug another organization has", viewer: ada, method: "POST", path: fixed("/v1/orgs"), body: `{"slug":"acme","name":"Acme","ownerEmail":"max@example.com"}`, wantStatus: 409, wantCode: "slug.taken"},
		{name: "an invalid slug", viewer: ada, method: "POST", path: fixed("/v1/orgs"), body: `{"slug":"-x","name":"X","ownerEmail":"max@example.com"}`, wantStatus: 400, wantCode: "slug.invalid"},
		{name: "an email nobody holds", viewer: ada, method: "POST", path: fixed("/v1/orgs"), body: `{"slug":"initech","name":"Initech","ownerEmail":"nobody@example.com"}`, wantStatus: 400, wantCode: "owner.not_found"},
		{name: "a body that isn't JSON", viewer: ada, method: "POST", path: fixed("/v1/orgs"), body: `slug=initech`, wantStatus: 400, wantCode: "request.invalid"},
		{name: "not without installation.users.manage", viewer: olivia, method: "POST", path: fixed("/v1/orgs"), body: `{"slug":"initech","name":"Initech","ownerEmail":"max@example.com"}`, wantStatus: 403, wantCode: "auth.forbidden"},
	})
}

func TestEnterOrganization(t *testing.T) {
	runCases(t, []apiCase{
		{name: "an organization the administrator isn't in", viewer: ada, method: "POST", path: fixed("/v1/orgs/acme/entries"), body: `{"reason":" Support ticket 42 "}`, wantStatus: 201,
			check: func(t *testing.T, f *fixture, rec *httptest.ResponseRecorder) {
				got := decode[Entry](t, rec.Body.Bytes())
				if got != (Entry{OrgId: f.acme.ID, Slug: "acme", Name: "Acme", Reason: "Support ticket 42"}) {
					t.Errorf("entry = %+v", got)
				}
				if want := []orgstest.Entry{{Org: f.acme.ID, User: f.ada.User.ID, Reason: "Support ticket 42"}}; !slices.Equal(f.store.Entered, want) {
					t.Errorf("entries = %v, want %v", f.store.Entered, want)
				}
			}},
		{name: "no reason", viewer: ada, method: "POST", path: fixed("/v1/orgs/acme/entries"), body: `{"reason":""}`, wantStatus: 400, wantCode: "reason.invalid"},
		{name: "an organization that isn't there", viewer: ada, method: "POST", path: fixed("/v1/orgs/nope/entries"), body: `{"reason":"Support"}`, wantStatus: 404, wantCode: "organization.not_found"},
		{name: "not without installation.manage", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/entries"), body: `{"reason":"Support"}`, wantStatus: 403, wantCode: "auth.forbidden"},
	})
}

func TestListUsers(t *testing.T) {
	runCases(t, []apiCase{
		{name: "a search by email", viewer: ada, method: "GET", path: fixed("/v1/users?q=olivia@"), wantStatus: 200,
			check: func(t *testing.T, f *fixture, rec *httptest.ResponseRecorder) {
				got := decode[struct {
					Users []User `json:"users"`
				}](t, rec.Body.Bytes())
				if len(got.Users) != 1 || got.Users[0].Id != f.olivia.User.ID || got.Users[0].Organizations != 2 || got.Users[0].Roles == nil || got.Users[0].Issuers == nil {
					t.Errorf("users = %s, want Olivia in two organizations, with lists never null", rec.Body)
				}
			}},
		{name: "a first page names the next", viewer: ada, method: "GET", path: fixed("/v1/users?limit=1"), wantStatus: 200,
			check: func(t *testing.T, _ *fixture, rec *httptest.ResponseRecorder) {
				if !strings.HasPrefix(rec.Header().Get("Link"), "<?cursor=") {
					t.Errorf("Link = %q, want the next page", rec.Header().Get("Link"))
				}
			}},
		{name: "not without installation.users.manage", viewer: olivia, method: "GET", path: fixed("/v1/users"), wantStatus: 403, wantCode: "auth.forbidden"},
	})
}
