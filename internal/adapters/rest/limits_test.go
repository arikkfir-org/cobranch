package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/limits"
	"cobranch.dev/internal/services/repos"
)

// fixedLimits are the same limits for the installation and every organization.
type fixedLimits limits.Limits

func (l fixedLimits) Installation(context.Context) (limits.Limits, error) {
	return limits.Limits(l), nil
}

func (l fixedLimits) Organization(context.Context, uuid.UUID) (limits.Limits, error) {
	return limits.Limits(l), nil
}

func TestLimitRefusals(t *testing.T) {
	tests := []struct {
		name, path, body, wantCode, wantDetail string
	}{
		{name: "an organization over the user's limit", path: "/v1/orgs", body: `{"slug":"initech","name":"Initech"}`, wantCode: "limit.organizations",
			wantDetail: "You created 1 organization, and a user may create at most 1 here. An administrator can create more for you."},
		{name: "a repository over the organization's limit", path: "/v1/orgs/acme/repos", body: `{"slug":"tools"}`, wantCode: "limit.repositories",
			wantDetail: "This organization holds 1 repository, and it may hold at most 1. An administrator can raise its limit."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newOrgFixture(t) // Acme holds website
			f.store.CreatedBy[uuid.New()] = f.nia.User.ID
			f.service.WithLimits(fixedLimits{OrganizationsPerUser: 1, RepositoriesPerOrganization: 1})
			f.repos.WithLimits(fixedLimits{OrganizationsPerUser: 1, RepositoriesPerOrganization: 1})
			viewer := &f.nia
			if strings.Contains(tt.path, "repos") {
				viewer = &f.olivia
			}
			rec := f.call(viewer, "POST", tt.path, tt.body)
			if rec.Code != 409 {
				t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body)
			}
			checkBody(t, rec, "", tt.wantCode)
			if problem := decode[struct{ Detail string }](t, rec.Body.Bytes()); problem.Detail != tt.wantDetail {
				t.Errorf("detail = %q, want %q", problem.Detail, tt.wantDetail)
			}
		})
	}
}

// newUsageFixture is Acme holding website, where Max is a Developer, and secret, which nobody but Olivia reads, with
// 1000 and 2000 bytes of Git data; Nia created two organizations herself and one an administrator made for her. The
// installation allows 5 organizations per user, and Acme 10 repositories and 1 GiB of Git data.
func newUsageFixture(t *testing.T) (*orgFixture, repos.Repository) {
	t.Helper()
	f := newOrgFixture(t)
	secret := repos.Repository{ID: uuid.New(), OrgID: f.acme.ID, Slug: "secret", Name: "Secret", ObjectFormat: "sha1", CreatedBy: f.olivia.User.ID, CreatedAt: now}
	if err := f.repoStore.Create(t.Context(), f.olivia.User.ID, secret, 0); err != nil {
		t.Fatal(err)
	}
	f.store.Grant(f.max.User.ID, auth.RoleDeveloper, auth.ScopeRepository, f.website.ID)
	f.repoStore.GitData[f.website.ID], f.repoStore.GitData[secret.ID] = 1000, 2000
	f.store.CreatedBy[uuid.New()], f.store.CreatedBy[uuid.New()] = f.nia.User.ID, f.nia.User.ID
	l := fixedLimits{OrganizationsPerUser: 5, RepositoriesPerOrganization: 10, GitDataPerOrganization: 1 << 30}
	f.service.WithLimits(l)
	f.repos.WithLimits(l)
	return f, secret
}

func TestGetMyUsage(t *testing.T) {
	tests := []struct {
		name       string
		viewer     func(*orgFixture) *auth.Authenticated
		want       ViewerUsage
		wantStatus int
		wantCode   string
	}{
		{name: "those Nia created herself, against the limit", viewer: nia, wantStatus: 200,
			want: ViewerUsage{Organizations: Usage{Used: 2, Limit: 5}}},
		{name: "none for one who created none", viewer: olivia, wantStatus: 200, want: ViewerUsage{Organizations: Usage{Limit: 5}}},
		{name: "anonymous", wantStatus: 401, wantCode: "auth.unauthenticated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := newUsageFixture(t)
			var viewer *auth.Authenticated
			if tt.viewer != nil {
				viewer = tt.viewer(f)
			}
			rec := f.call(viewer, "GET", "/v1/me/usage", "")
			checkUsageAnswer(t, f, viewer, "/v1/me/usage", rec, tt.wantStatus, tt.wantCode)
			if tt.wantStatus == 200 {
				if got := decode[ViewerUsage](t, rec.Body.Bytes()); got != tt.want {
					t.Errorf("usage = %+v, want %+v", got, tt.want)
				}
			}
		})
	}
}

func TestGetOrganizationUsage(t *testing.T) {
	tests := []struct {
		name       string
		viewer     func(*orgFixture) *auth.Authenticated
		path       string
		want       []RepositoryUsage // the repositories listed, without their IDs
		wantStatus int
		wantCode   string
	}{
		{name: "an Owner sees every repository's Git data", viewer: olivia, path: "/v1/orgs/acme/usage", wantStatus: 200,
			want: []RepositoryUsage{{Slug: "secret", Name: "Secret", GitData: 2000}, {Slug: "website", Name: "Website", GitData: 1000}}},
		{name: "a member sees the totals, and only the repositories they read", viewer: maxim, path: "/v1/orgs/acme/usage", wantStatus: 200,
			want: []RepositoryUsage{{Slug: "website", Name: "Website", GitData: 1000}}},
		{name: "by an old slug", viewer: maxim, path: "/v1/orgs/acme-corp/usage", wantStatus: 200,
			want: []RepositoryUsage{{Slug: "website", Name: "Website", GitData: 1000}}},
		{name: "outsiders don't learn the organization exists", viewer: nia, path: "/v1/orgs/acme/usage", wantStatus: 404, wantCode: "organization.not_found"},
		{name: "anonymous", path: "/v1/orgs/acme/usage", wantStatus: 401, wantCode: "auth.unauthenticated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := newUsageFixture(t)
			var viewer *auth.Authenticated
			if tt.viewer != nil {
				viewer = tt.viewer(f)
			}
			rec := f.call(viewer, "GET", tt.path, "")
			checkUsageAnswer(t, f, viewer, tt.path, rec, tt.wantStatus, tt.wantCode)
			if tt.wantStatus != 200 {
				return
			}
			got := decode[OrganizationUsage](t, rec.Body.Bytes())
			if got.Repositories != (Usage{Used: 2, Limit: 10}) || got.GitData != (Usage{Used: 3000, Limit: 1 << 30}) {
				t.Errorf("usage = %+v, want 2 of 10 repositories and 3000 bytes of 1 GiB", got)
			}
			for i := range got.ByRepository {
				got.ByRepository[i].Id = uuid.Nil
			}
			if !slices.Equal(got.ByRepository, tt.want) {
				t.Errorf("byRepository = %+v, want %+v", got.ByRepository, tt.want)
			}
		})
	}
}

// checkUsageAnswer checks a usage answer's status and problem code, and that a 200 carries an ETag that answers 304
// when asked again with it.
func checkUsageAnswer(t *testing.T, f *orgFixture, viewer *auth.Authenticated, path string, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d: %s", rec.Code, wantStatus, rec.Body)
	}
	if wantCode != "" {
		checkBody(t, rec, "", wantCode)
	}
	if wantStatus != 200 {
		return
	}
	tag := rec.Header().Get("ETag")
	if tag == "" {
		t.Fatal("no ETag")
	}
	again := f.callWith(viewer, "GET", path, "", http.Header{"If-None-Match": {tag}})
	if again.Code != 304 || again.Header().Get("ETag") != tag {
		t.Errorf("asked again with its ETag: %d, ETag %q, want 304 and %q", again.Code, again.Header().Get("ETag"), tag)
	}
}
