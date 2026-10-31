package rest

import (
	"slices"
	"testing"

	"cobranch.dev/internal/services/auth"
)

func TestRepositoriesAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "an Owner lists every repository", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[struct{ Repositories []Repository }](t, body)
				if len(got.Repositories) != 1 || got.Repositories[0].Slug != "website" || got.Repositories[0].Visibility != "private" ||
					!slices.Contains(got.Repositories[0].ViewerPermissions, "repo.manage") {
					t.Errorf("repositories = %s", body)
				}
			}},
		{name: "a Member lists none", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/repos"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ Repositories []Repository }](t, body); got.Repositories == nil || len(got.Repositories) != 0 {
					t.Errorf("repositories = %s, want []", body)
				}
			}},
		{name: "an outsider lists nothing", viewer: nia, method: "GET", path: fixed("/v1/orgs/acme/repos"), wantStatus: 404, wantCode: "organization.not_found"},
		{name: "a Member creates one", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/repos"), body: `{"slug":"tools","description":"Scripts"}`, wantStatus: 201,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				r := decode[Repository](t, body)
				if r.Slug != "tools" || r.Name != "tools" || r.DefaultBranch != "main" || r.ObjectFormat != "sha1" || r.CreatedBy == nil || *r.CreatedBy != f.max.User.ID ||
					!slices.Contains(r.ViewerPermissions, "repo.manage") {
					t.Errorf("repository = %s", body)
				}
			}},
		{name: "a taken slug", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/repos"), body: `{"slug":"website"}`, wantStatus: 409, wantCode: "slug.taken"},
		{name: "a reserved slug", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/repos"), body: `{"slug":"settings"}`, wantStatus: 400, wantCode: "slug.reserved"},
		{name: "an outsider creates nothing", viewer: nia, method: "POST", path: fixed("/v1/orgs/acme/repos"), body: `{"slug":"tools"}`, wantStatus: 404, wantCode: "organization.not_found"},
		{name: "get through the organization's old slug", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme-corp/repos/website"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if r := decode[Repository](t, body); r.Slug != "website" || r.Name != "Website" {
					t.Errorf("repository = %s", body)
				}
			}},
		{name: "a Member doesn't see it", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/repos/website"), wantStatus: 404, wantCode: "repository.not_found"},
		{name: "rename, keeping the rest", viewer: olivia, method: "PATCH", path: fixed("/v1/orgs/acme/repos/website"), body: `{"slug":"www"}`, wantStatus: 200,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				if r := decode[Repository](t, body); r.Slug != "www" || r.Name != "Website" || r.DefaultBranch != "main" {
					t.Errorf("repository = %s", body)
				}
				if rec := f.call(&f.olivia, "GET", "/v1/orgs/acme/repos/website", ""); rec.Code != 200 || decode[Repository](t, rec.Body.Bytes()).Slug != "www" {
					t.Errorf("the old slug answers %d: %s", rec.Code, rec.Body)
				}
			}},
		{name: "a bad branch", viewer: olivia, method: "PATCH", path: fixed("/v1/orgs/acme/repos/website"), body: `{"defaultBranch":"a b"}`, wantStatus: 400, wantCode: "branch.invalid"},
	})
}

func TestRepositoryAccessAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "the roles it grants", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/roles"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[struct{ Roles []Role }](t, body)
				if len(got.Roles) != 4 || got.Roles[0].Name != "Reader" {
					t.Errorf("roles = %s", body)
				}
			}},
		{name: "who holds grants on it", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/access"), wantStatus: 200,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				got := decode[struct{ Access []RepositoryAccess }](t, body)
				if len(got.Access) != 1 || got.Access[0].PrincipalId != f.olivia.User.ID || got.Access[0].Kind != "user" || got.Access[0].Roles[0].Name != "Maintainer" {
					t.Errorf("access = %s", body)
				}
			}},
		{name: "grant a Member Developer, who then reads it", viewer: olivia, method: "POST",
			path: func(f *orgFixture) string {
				return "/v1/orgs/acme/repos/website/access/" + f.max.User.ID.String() + "/roles"
			},
			body: `{"roleId":"` + auth.RoleDeveloper.String() + `"}`, wantStatus: 204,
			check: func(t *testing.T, f *orgFixture, _ []byte) {
				if rec := f.call(&f.max, "GET", "/v1/orgs/acme/repos/website", ""); rec.Code != 200 || !slices.Contains(decode[Repository](t, rec.Body.Bytes()).ViewerPermissions, "repo.push") {
					t.Errorf("Max gets %d: %s", rec.Code, rec.Body)
				}
			}},
		{name: "grant a role held", viewer: olivia, method: "POST",
			path: func(f *orgFixture) string {
				return "/v1/orgs/acme/repos/website/access/" + f.olivia.User.ID.String() + "/roles"
			},
			body: `{"roleId":"` + auth.RoleMaintainer.String() + `"}`, wantStatus: 409, wantCode: "grant.exists"},
		{name: "grant someone outside the organization", viewer: olivia, method: "POST",
			path: func(f *orgFixture) string {
				return "/v1/orgs/acme/repos/website/access/" + f.nia.User.ID.String() + "/roles"
			},
			body: `{"roleId":"` + auth.RoleReader.String() + `"}`, wantStatus: 404, wantCode: "principal.not_found"},
		{name: "grant an organization role", viewer: olivia, method: "POST",
			path: func(f *orgFixture) string {
				return "/v1/orgs/acme/repos/website/access/" + f.max.User.ID.String() + "/roles"
			},
			body: `{"roleId":"` + auth.RoleOwner.String() + `"}`, wantStatus: 400, wantCode: "role.invalid"},
		{name: "revoke", viewer: olivia, method: "DELETE",
			path: func(f *orgFixture) string {
				return "/v1/orgs/acme/repos/website/access/" + f.olivia.User.ID.String() + "/roles/" + auth.RoleMaintainer.String()
			}, wantStatus: 204},
		{name: "revoke a role not held", viewer: olivia, method: "DELETE",
			path: func(f *orgFixture) string {
				return "/v1/orgs/acme/repos/website/access/" + f.max.User.ID.String() + "/roles/" + auth.RoleReader.String()
			}, wantStatus: 404, wantCode: "grant.not_found"},
	})
}
