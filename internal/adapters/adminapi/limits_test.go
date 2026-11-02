package adminapi

import (
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/limits"
)

// of points at n.
func of(n int64) *int64 { return &n }

// newFixtureLimited is the fixture under the limits the values set.
func newFixtureLimited(t *testing.T, values limits.Settings) *fixture {
	t.Helper()
	f := newFixture(t)
	service := admin.NewService(f.store, auth.NewAuthorizer(f.store), auth.Settings{}).WithLimits(values)
	service.SetClock(func() time.Time { return now })
	f.handler = NewHandler("v1", service)
	return f
}

// readLimits GETs Acme's limits as Ada and returns their ETag.
func (f *fixture) readLimits(t *testing.T) string {
	t.Helper()
	rec := f.call(&f.ada, "GET", "/v1/orgs/acme/limits", "")
	if rec.Code != 200 || rec.Header().Get("ETag") == "" {
		t.Fatalf("GET /v1/orgs/acme/limits = %d with ETag %q: %s", rec.Code, rec.Header().Get("ETag"), rec.Body)
	}
	return rec.Header().Get("ETag")
}

func TestSettingsLimits(t *testing.T) {
	f := newFixtureLimited(t, limits.Settings{GitDataPerOrganization: of(5 << 30)})
	got := decode[Settings](t, f.call(&f.ada, "GET", "/v1/settings", "").Body.Bytes())
	if got.OrganizationsPerUser != 5 || got.RepositoriesPerOrganization != 100 || got.GitDataPerOrganization != 5<<30 ||
		!slices.Equal(got.Locked, []SettingsLocked{GitDataPerOrganization}) {
		t.Errorf("settings = %+v, want the defaults and the values' Git data, locked", got)
	}
	header := map[string]string{"Content-Type": "application/merge-patch+json", "If-Match": f.read(t)}
	rec := f.callWith(&f.ada, "PATCH", "/v1/settings", `{"organizationsPerUser":0,"repositoriesPerOrganization":500}`, header)
	check(t, rec, 200, "")
	if got := decode[Settings](t, rec.Body.Bytes()); got.OrganizationsPerUser != 0 || got.RepositoriesPerOrganization != 500 {
		t.Errorf("settings = %+v, want no organizations limit and 500 repositories", got)
	}
	header["If-Match"] = f.read(t)
	check(t, f.callWith(&f.ada, "PATCH", "/v1/settings", `{"gitDataPerOrganization":0}`, header), 409, "setting.locked")
	check(t, f.callWith(&f.ada, "PATCH", "/v1/settings", `{"repositoriesPerOrganization":-1}`, header), 400, "limit.invalid")
}

func TestOrganizationsUsage(t *testing.T) {
	f := newFixture(t)
	f.store.Repos[uuid.New()] = f.acme.ID
	f.store.GitData[f.acme.ID] = 3 << 20
	f.store.Limits[f.acme.ID] = admin.OwnLimits{Overrides: limits.Overrides{Repositories: of(0)}, Version: 1}
	rec := f.call(&f.ada, "GET", "/v1/orgs", "")
	check(t, rec, 200, "")
	for _, o := range decode[struct{ Organizations []Organization }](t, rec.Body.Bytes()).Organizations {
		want := map[string][2]Usage{
			"acme":   {{Used: 1, Limit: 0}, {Used: 3 << 20, Limit: 1 << 30}},
			"globex": {{Used: 0, Limit: 100}, {Used: 0, Limit: 1 << 30}},
		}[o.Slug]
		if o.Repositories != want[0] || o.GitData != want[1] {
			t.Errorf("%s: repositories %+v and Git data %+v, want %+v and %+v", o.Slug, o.Repositories, o.GitData, want[0], want[1])
		}
	}
}

func TestGetOrganizationLimits(t *testing.T) {
	tests := []struct {
		name       string
		own        admin.OwnLimits
		viewer     func(*fixture) *auth.Authenticated
		path       string
		noneMatch  bool
		wantStatus int
		wantCode   string
		want       [2]OrganizationLimit
	}{
		{name: "the installation's", viewer: ada, wantStatus: 200,
			want: [2]OrganizationLimit{{Installation: 100, Applies: 100}, {Installation: 1 << 30, Applies: 1 << 30}}},
		{name: "its own", own: admin.OwnLimits{Overrides: limits.Overrides{GitData: of(0)}, Version: 1, UpdatedAt: now}, viewer: ada, wantStatus: 200,
			want: [2]OrganizationLimit{{Installation: 100, Applies: 100}, {Own: of(0), Installation: 1 << 30, Applies: 0}}},
		{name: "unchanged since the caller's ETag", viewer: ada, noneMatch: true, wantStatus: 304},
		{name: "an organization that isn't there", viewer: ada, path: "/v1/orgs/initech/limits", wantStatus: 404, wantCode: "organization.not_found"},
		{name: "not without installation.manage", viewer: olivia, wantStatus: 403, wantCode: "auth.forbidden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.Limits[f.acme.ID] = tt.own
			path, header := "/v1/orgs/acme/limits", map[string]string{}
			if tt.path != "" {
				path = tt.path
			}
			if tt.noneMatch {
				header["If-None-Match"] = f.readLimits(t)
			}
			rec := f.callWith(tt.viewer(f), "GET", path, "", header)
			check(t, rec, tt.wantStatus, tt.wantCode)
			if tt.wantStatus != 200 {
				return
			}
			got := decode[OrganizationLimits](t, rec.Body.Bytes())
			if got.OrgId != f.acme.ID || !sameLimit(got.Repositories, tt.want[0]) || !sameLimit(got.GitData, tt.want[1]) ||
				(got.UpdatedAt != nil) != (tt.own.Version > 0) {
				t.Errorf("limits = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// sameLimit reports whether a and b say the same.
func sameLimit(a, b OrganizationLimit) bool {
	return a.Installation == b.Installation && a.Applies == b.Applies && (a.Own == nil) == (b.Own == nil) && (a.Own == nil || *a.Own == *b.Own)
}

func TestChangeOrganizationLimits(t *testing.T) {
	tests := []struct {
		name       string
		own        admin.OwnLimits
		viewer     func(*fixture) *auth.Authenticated
		path       string
		ifMatch    func(etag string) string
		body       string
		wantStatus int
		wantCode   string
		want       limits.Overrides
	}{
		{name: "one set", viewer: ada, ifMatch: same, body: `{"repositories":500}`, wantStatus: 200, want: limits.Overrides{Repositories: of(500)}},
		{name: "one cleared, the other left", own: admin.OwnLimits{Overrides: limits.Overrides{Repositories: of(500), GitData: of(0)}, Version: 1},
			viewer: ada, ifMatch: same, body: `{"gitData":null}`, wantStatus: 200, want: limits.Overrides{Repositories: of(500)}},
		{name: "below 0", viewer: ada, ifMatch: same, body: `{"gitData":-1}`, wantStatus: 400, wantCode: "limit.invalid"},
		{name: "not a number", viewer: ada, ifMatch: same, body: `{"gitData":"1Gi"}`, wantStatus: 400, wantCode: "request.invalid"},
		{name: "no If-Match", viewer: ada, body: `{"repositories":500}`, wantStatus: 428, wantCode: "precondition.required"},
		{name: "an old ETag", viewer: ada, ifMatch: func(string) string { return `"0123"` }, body: `{"repositories":500}`, wantStatus: 412, wantCode: "precondition.failed"},
		{name: "an organization that isn't there", viewer: ada, path: "/v1/orgs/initech/limits", ifMatch: func(string) string { return "*" },
			body: `{"repositories":500}`, wantStatus: 404, wantCode: "organization.not_found"},
		{name: "not without installation.manage", viewer: olivia, ifMatch: same, body: `{"repositories":500}`, wantStatus: 403, wantCode: "auth.forbidden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.Limits[f.acme.ID] = tt.own
			path, header := "/v1/orgs/acme/limits", map[string]string{"Content-Type": "application/merge-patch+json"}
			if tt.path != "" {
				path = tt.path
			}
			if tt.ifMatch != nil {
				header["If-Match"] = tt.ifMatch(f.readLimits(t))
			}
			rec := f.callWith(tt.viewer(f), "PATCH", path, tt.body, header)
			check(t, rec, tt.wantStatus, tt.wantCode)
			if tt.wantStatus != 200 {
				if stored := f.store.Limits[f.acme.ID]; stored != tt.own {
					t.Errorf("a refused change was written: %+v", stored)
				}
				return
			}
			checkChangedLimits(t, f, rec, tt.own.Version+1, tt.want)
		})
	}
}

// checkChangedLimits fails t unless rec answers Acme's own limits want, stored at version, by Ada, with their ETag.
func checkChangedLimits(t *testing.T, f *fixture, rec *httptest.ResponseRecorder, version int64, want limits.Overrides) {
	t.Helper()
	got, stored := decode[OrganizationLimits](t, rec.Body.Bytes()), f.store.Limits[f.acme.ID]
	if !sameLimit(got.Repositories, OrganizationLimit{Own: want.Repositories, Installation: 100, Applies: applies(want.Repositories, 100)}) ||
		!sameLimit(got.GitData, OrganizationLimit{Own: want.GitData, Installation: 1 << 30, Applies: applies(want.GitData, 1<<30)}) ||
		got.UpdatedBy == nil || *got.UpdatedBy != f.ada.User.ID || stored.Version != version {
		t.Errorf("limits = %+v, stored %+v; want its own %+v", got, stored, want)
	}
	if rec.Header().Get("ETag") != f.readLimits(t) {
		t.Errorf("the change's ETag %s isn't the limits' now", rec.Header().Get("ETag"))
	}
}

// applies is own, else installation.
func applies(own *int64, installation int64) int64 {
	if own != nil {
		return *own
	}
	return installation
}
