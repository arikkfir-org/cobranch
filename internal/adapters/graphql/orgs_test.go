package graphql

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/markdown"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/code/codetest"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/repos"
	"cobranch.dev/internal/services/repos/repostest"
)

// emptyOrgs returns the organizations' service over an empty store.
func emptyOrgs() *orgs.Service {
	store := orgstest.NewStore()
	return orgs.NewService(store, auth.NewAuthorizer(store))
}

// orgFixture is Acme, with Olivia its Owner and Max a Member, Nia outside it with a pending invitation and
// permission to create organizations, over the organizations' service; website is Acme's repository, which Olivia
// created, over the repositories', with main at c after a ← b ← c and feature at d on a, all pushed by Olivia, over the
// code's.
type orgFixture struct {
	store            *orgstest.Store
	service          *orgs.Service
	repoStore        *repostest.Store
	repos            *repos.Service
	acme             orgs.Organization
	olivia, max, nia auth.Authenticated
	invitation       orgs.Invitation
	website          repos.Repository
	codeStore        *codetest.Store
	objects          *codetest.Objects
	code             *code.Service
	push             code.Push
}

func viewerOf(name, email string) auth.Authenticated {
	return auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: name},
		Emails: []auth.Email{{Email: email, Verified: true}}}}
}

func newOrgFixture() *orgFixture {
	store := orgstest.NewStore()
	f := &orgFixture{store: store, service: orgs.NewService(store, auth.NewAuthorizer(store))}
	f.service.SetClock(func() time.Time { return now })
	f.olivia, f.max, f.nia = viewerOf("Olivia", "olivia@example.com"), viewerOf("Max", "max@example.com"), viewerOf("Nia", "nia@example.com")
	for _, v := range []auth.Authenticated{f.olivia, f.max, f.nia} {
		store.AddUser(v.User.ID, v.User.DisplayName, v.User.Emails[0].Email)
	}
	store.Grant(f.nia.User.ID, auth.RoleInstallationUser, auth.ScopeInstallation, uuid.Nil)
	f.acme = orgs.Organization{ID: uuid.New(), Slug: "acme", Name: "Acme", MembershipRoleID: auth.RoleMember, AcceptsJoinRequests: true, CreatedAt: now}
	store.Orgs[f.acme.ID], store.Slugs["acme"], store.Slugs["acme-corp"] = f.acme, f.acme.ID, f.acme.ID
	store.AddMember(f.acme.ID, f.olivia.User.ID, auth.RoleOwner)
	store.AddMember(f.acme.ID, f.max.User.ID, auth.RoleMember)
	f.invitation = orgs.Invitation{ID: uuid.New(), OrgID: f.acme.ID, OrgSlug: "acme", OrgName: "Acme", Email: "nia@example.com", RoleID: auth.RoleMember,
		InvitedBy: f.olivia.User.ID, State: orgs.StatePending, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	store.Invites[f.invitation.ID] = f.invitation
	f.repoStore = repostest.NewStore(store)
	f.repos = repos.NewService(f.repoStore, auth.NewAuthorizer(store))
	f.website = repos.Repository{ID: uuid.New(), OrgID: f.acme.ID, Slug: "website", Name: "Website", ObjectFormat: "sha1",
		CreatedBy: f.olivia.User.ID, CreatedAt: now}
	if err := f.repoStore.Create(context.Background(), f.olivia.User.ID, f.website); err != nil {
		panic(err)
	}
	f.codeStore, f.objects = codetest.NewStore(), codetest.NewObjects()
	f.code = code.NewService(f.codeStore, f.objects, markdown.New(nil, nil), auth.NewAuthorizer(store))
	f.push = code.Push{ID: uuid.New(), Pusher: &code.Principal{ID: f.olivia.User.ID, DisplayName: "Olivia"}, Transport: git.TransportHTTPS, At: now}
	f.codeStore.Line(f.website.ID, f.push, shaOf("a"), shaOf("b"), shaOf("c"))
	f.codeStore.Line(f.website.ID, f.push, shaOf("a"), shaOf("d"))
	f.codeStore.SetBranch(f.website.ID, "main", shaOf("c"))
	f.codeStore.SetBranch(f.website.ID, "feature", shaOf("d"))
	f.codeStore.SetBranch(f.website.ID, "feature/login", shaOf("c"))
	f.codeStore.UpdateBranch(f.website.ID, "main", func(b *code.Branch) {
		b.Requirements = &git.Requirements{Branch: "main", Annotations: []string{"ci/build"}}
	})
	f.codeStore.UpdateBranch(f.website.ID, "feature", func(b *code.Branch) {
		b.Base, b.Status, b.Facts = "main", code.StatusBlocked, &git.Facts{Branch: "feature", Ahead: 1, Behind: 2}
		b.Checks = []code.Check{{Kind: code.CheckAnnotation, Name: "ci/build"}, {Kind: code.CheckUpToDate, Count: 2}}
		// Merged once at a, since moved on.
		b.Merge = &code.Merge{Head: shaOf("a"), By: &code.Principal{ID: f.olivia.User.ID, DisplayName: "Olivia"}, At: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}
	})
	f.codeStore.UpdateBranch(f.website.ID, "feature/login", func(b *code.Branch) { b.Base, b.Status = "main", code.StatusProcessing })
	f.codeStore.TagList[f.website.ID] = []code.Tag{{Name: "v1.0.0", Target: shaOf("b")}}
	f.codeStore.SlugList[f.website.ID] = [2]string{"acme", "website"}
	f.objects.Commit(shaOf("c"), map[string]string{"README.md": "# Website\n", "docs/guide.md": "Read me.\n", "docs/deep/notes.txt": "Notes\n",
		"logo.png": "\x89PNG\x00\x01", "latest" + codetest.Symlink: "docs/guide.md", "vendor/lib" + codetest.Submodule: shaOf("e")}, "main")
	f.objects.Commit(shaOf("b"), map[string]string{"README.md": "# Old\n"}, "v1.0.0")
	logged := func(letter string, parents ...string) code.LogEntry {
		e := code.LogEntry{SHA: shaOf(letter), Parents: []string{}}
		for _, p := range parents {
			e.Parents = append(e.Parents, shaOf(p))
		}
		return e
	}
	f.objects.History(shaOf("c"), []code.LogEntry{logged("c", "b"), logged("b", "a"), logged("a")})
	f.objects.History(shaOf("b"), []code.LogEntry{logged("b", "a"), logged("a")})
	return f
}

// shaOf is the object name of 40 letter's.
func shaOf(letter string) string { return strings.Repeat(letter, 40) }

// query serves one GraphQL document as viewer and returns the response's body.
func (f *orgFixture) query(viewer *auth.Authenticated, document string) string {
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"query":`+jsonString(document)+`}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	as(viewer, NewHandler("v1", nil, f.service, f.repos, f.code)).ServeHTTP(rec, req)
	return rec.Body.String()
}

type orgCase struct {
	name     string
	viewer   func(f *orgFixture) *auth.Authenticated
	document func(f *orgFixture) string
	// want are substrings of the response; wantCode an error's code, wantReason its reason.
	want                 []string
	wantCode, wantReason string
}

func runOrgCases(t *testing.T, cases []orgCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newOrgFixture()
			var viewer *auth.Authenticated
			if tt.viewer != nil {
				viewer = tt.viewer(f)
			}
			body := f.query(viewer, tt.document(f))
			if tt.wantCode == "" && tt.wantReason == "" && strings.Contains(body, `"errors"`) {
				t.Errorf("body = %s, want no errors", body)
			}
			for _, w := range tt.wanted() {
				if !strings.Contains(body, w) {
					t.Errorf("body = %s, want %s in it", body, w)
				}
			}
		})
	}
}

// wanted is every substring the response must hold: want, plus the error's code and reason when the case expects one.
func (tt orgCase) wanted() []string {
	want := tt.want
	if tt.wantCode != "" {
		want = append(want, `"code":"`+tt.wantCode+`"`)
	}
	if tt.wantReason != "" {
		want = append(want, `"reason":"`+tt.wantReason+`"`)
	}
	return want
}

func olivia(f *orgFixture) *auth.Authenticated { return &f.olivia }
func maxim(f *orgFixture) *auth.Authenticated  { return &f.max }
func nia(f *orgFixture) *auth.Authenticated    { return &f.nia }
func fixed(document string) func(*orgFixture) string {
	return func(*orgFixture) string { return document }
}

func TestOrganizationQueries(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "the viewer's organizations and permissions", viewer: olivia,
			document: fixed(`{ viewer { permissions organizations { slug name viewerPermissions acceptsJoinRequests } } }`),
			want:     []string{`"permissions":[]`, `"slug":"acme"`, `"org.manage"`, `"acceptsJoinRequests":true`}},
		{name: "Nia may create organizations", viewer: nia, document: fixed(`{ viewer { permissions organizations { slug } invitations { organizationSlug email } } }`),
			want: []string{`"permissions":["orgs.create"]`, `"organizations":[]`, `"organizationSlug":"acme","email":"nia@example.com"`}},
		{name: "an organization by a former slug", viewer: maxim, document: fixed(`{ organization(slug: "acme-corp") { slug members { displayName roles { name } } } }`),
			want: []string{`"slug":"acme"`, `"displayName":"Max","roles":[{"name":"Member"}]`, `"displayName":"Olivia","roles":[{"name":"Owner"}]`}},
		{name: "an organization the viewer isn't in", viewer: nia, document: fixed(`{ organization(slug: "acme") { slug } }`), want: []string{`{"data":{"organization":null}}`}},
		{name: "a member may not list invitations", viewer: maxim, document: fixed(`{ organization(slug: "acme") { invitations { email } } }`), wantCode: forbidden},
		{name: "an Owner lists invitations", viewer: olivia, document: fixed(`{ organization(slug: "acme") { invitations { email roleId } joinRequests { id } } }`),
			want: []string{`"email":"nia@example.com"`, `"joinRequests":[]`}},
		{name: "roles and permissions", viewer: maxim, document: fixed(`{ permissions(scope: ORGANIZATION) organization(slug: "acme") { roles { name builtIn organizationId } } }`),
			want: []string{`"permissions":["org.read","org.manage"`, `{"name":"Owner","builtIn":true,"organizationId":null}`, `{"name":"Manager","builtIn":false,"organizationId":null}`}},
		{name: "anonymous", document: fixed(`{ organization(slug: "acme") { slug } }`), wantCode: unauthenticated},
	})
}

func TestOrganizationMutations(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "create", viewer: nia, document: fixed(`mutation { createOrganization(slug: "globex", name: "Globex") { slug viewerPermissions } }`),
			want: []string{`"slug":"globex"`, `"org.manage"`}},
		{name: "create without the permission", viewer: maxim, document: fixed(`mutation { createOrganization(slug: "globex", name: "Globex") { slug } }`), wantCode: forbidden},
		{name: "create with a taken slug", viewer: nia, document: fixed(`mutation { createOrganization(slug: "acme-corp", name: "Acme") { slug } }`), wantCode: conflict, wantReason: "slug.taken"},
		{name: "create with a bad slug", viewer: nia, document: fixed(`mutation { createOrganization(slug: "Acme!", name: "Acme") { slug } }`), wantCode: badUserInput, wantReason: "slug.invalid"},
		{name: "update", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { updateOrganization(id: "` + f.acme.ID.String() + `", input: {slug: "acme-inc", name: "Acme Inc", membershipRoleId: "` + auth.RoleGuest.String() + `", acceptsJoinRequests: false, providers: ["google"]}) { slug name providers acceptsJoinRequests } }`
		}, want: []string{`"slug":"acme-inc","name":"Acme Inc","providers":["google"],"acceptsJoinRequests":false`}},
		{name: "update as a member", viewer: maxim, document: func(f *orgFixture) string {
			return `mutation { updateOrganization(id: "` + f.acme.ID.String() + `", input: {slug: "acme", name: "Acme", membershipRoleId: "` + auth.RoleMember.String() + `", acceptsJoinRequests: true}) { slug } }`
		}, wantCode: forbidden},
		{name: "update nonsense", viewer: olivia, document: fixed(`mutation { updateOrganization(id: "nope", input: {slug: "acme", name: "Acme", membershipRoleId: "x", acceptsJoinRequests: true}) { slug } }`),
			wantCode: notFound, wantReason: "organization.not_found"},
	})
}

func TestMembershipMutations(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "invite", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { inviteMember(organizationId: "` + f.acme.ID.String() + `", email: "Pat@Example.com", roleId: "` + auth.RoleGuest.String() + `") { email organizationSlug } }`
		}, want: []string{`"email":"pat@example.com","organizationSlug":"acme"`}},
		{name: "invite as a member", viewer: maxim, document: func(f *orgFixture) string {
			return `mutation { inviteMember(organizationId: "` + f.acme.ID.String() + `", email: "pat@example.com", roleId: "` + auth.RoleGuest.String() + `") { email } }`
		}, wantCode: forbidden},
		{name: "invite twice", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { inviteMember(organizationId: "` + f.acme.ID.String() + `", email: "nia@example.com", roleId: "` + auth.RoleGuest.String() + `") { email } }`
		}, wantCode: conflict, wantReason: "invitation.exists"},
		{name: "accept", viewer: nia, document: func(f *orgFixture) string {
			return `mutation { acceptInvitation(id: "` + f.invitation.ID.String() + `") { slug viewerPermissions } }`
		}, want: []string{`"slug":"acme","viewerPermissions":["agents.assign","agents.create","org.read","repos.create"]`}},
		{name: "accept another's invitation", viewer: maxim, document: func(f *orgFixture) string {
			return `mutation { acceptInvitation(id: "` + f.invitation.ID.String() + `") { slug } }`
		}, wantCode: notFound, wantReason: "invitation.not_found"},
		{name: "decline", viewer: nia, document: func(f *orgFixture) string {
			return `mutation { declineInvitation(id: "` + f.invitation.ID.String() + `") }`
		}, want: []string{`{"data":{"declineInvitation":true}}`}},
		{name: "cancel", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { cancelInvitation(organizationId: "` + f.acme.ID.String() + `", id: "` + f.invitation.ID.String() + `") }`
		}, want: []string{`{"data":{"cancelInvitation":true}}`}},
		{name: "request to join", viewer: nia, document: fixed(`mutation { requestToJoin(slug: "acme", note: "Hi") { organizationSlug userName note } }`),
			want: []string{`"organizationSlug":"acme","userName":"Nia","note":"Hi"`}},
		{name: "request to join an unknown organization", viewer: nia, document: fixed(`mutation { requestToJoin(slug: "nope") { id } }`), wantCode: notFound},
		{name: "leave", viewer: maxim, document: func(f *orgFixture) string {
			return `mutation { leaveOrganization(id: "` + f.acme.ID.String() + `") }`
		}, want: []string{`{"data":{"leaveOrganization":true}}`}},
		{name: "the last Owner can't leave", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { leaveOrganization(id: "` + f.acme.ID.String() + `") }`
		}, wantCode: conflict, wantReason: "org.last_owner"},
		{name: "remove a member", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { removeMember(organizationId: "` + f.acme.ID.String() + `", userId: "` + f.max.User.ID.String() + `") }`
		}, want: []string{`{"data":{"removeMember":true}}`}},
		{name: "remove as a member", viewer: maxim, document: func(f *orgFixture) string {
			return `mutation { removeMember(organizationId: "` + f.acme.ID.String() + `", userId: "` + f.olivia.User.ID.String() + `") }`
		}, wantCode: forbidden},
	})
}

func TestJoinRequestMutations(t *testing.T) {
	request := func(f *orgFixture) orgs.JoinRequest {
		req := orgs.JoinRequest{ID: uuid.New(), OrgID: f.acme.ID, OrgSlug: "acme", OrgName: "Acme", UserID: f.nia.User.ID, UserName: "Nia",
			State: orgs.StatePending, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
		f.store.Reqs[req.ID] = req
		return req
	}
	runOrgCases(t, []orgCase{
		{name: "approve", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { approveJoinRequest(organizationId: "` + f.acme.ID.String() + `", id: "` + request(f).ID.String() + `", roleId: "` + auth.RoleMember.String() + `") }`
		}, want: []string{`{"data":{"approveJoinRequest":true}}`}},
		{name: "approve as a member", viewer: maxim, document: func(f *orgFixture) string {
			return `mutation { approveJoinRequest(organizationId: "` + f.acme.ID.String() + `", id: "` + request(f).ID.String() + `", roleId: "` + auth.RoleMember.String() + `") }`
		}, wantCode: forbidden},
		{name: "decline", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { declineJoinRequest(organizationId: "` + f.acme.ID.String() + `", id: "` + request(f).ID.String() + `") }`
		}, want: []string{`{"data":{"declineJoinRequest":true}}`}},
		{name: "decline nonsense", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { declineJoinRequest(organizationId: "` + f.acme.ID.String() + `", id: "nope") }`
		}, wantCode: notFound, wantReason: "join request.not_found"},
		{name: "the viewer's requests", viewer: func(f *orgFixture) *auth.Authenticated { request(f); return &f.nia },
			document: fixed(`{ viewer { joinRequests { organizationSlug userName } } }`), want: []string{`"organizationSlug":"acme","userName":"Nia"`}},
	})
}
