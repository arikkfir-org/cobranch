package graphql

import (
	"testing"

	"cobranch.dev/internal/services/auth"
)

func TestRepositoryQueries(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "an Owner's repositories, with their lazy fields", viewer: olivia,
			document: fixed(`{ organization(slug: "acme") { repositories { slug name visibility defaultBranch objectFormat viewerPermissions access { displayName kind roles { name } } roles { name } } } }`),
			want: []string{`"slug":"website"`, `"visibility":"PRIVATE"`, `"defaultBranch":"main"`, `"objectFormat":"sha1"`, `"repo.manage"`,
				`"access":[{"displayName":"Olivia","kind":"user","roles":[{"name":"Maintainer"}]}]`, `{"name":"Reader"},{"name":"Developer"}`}},
		{name: "a Member reads none", viewer: maxim, document: fixed(`{ organization(slug: "acme") { repositories { slug } } }`),
			want: []string{`"repositories":[]`}},
		{name: "a repository by slug", viewer: olivia, document: fixed(`{ organization(slug: "acme") { repository(slug: "website") { id name } } }`),
			want: []string{`"name":"Website"`}},
		{name: "a repository the viewer doesn't read is null", viewer: maxim, document: fixed(`{ organization(slug: "acme") { repository(slug: "website") { id } } }`),
			want: []string{`"repository":null`}},
	})
}

func TestRepositoryMutations(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a Member creates one", viewer: maxim,
			document: func(f *orgFixture) string {
				return `mutation { createRepository(organizationId: "` + f.acme.ID.String() + `", input: {slug: "tools", description: "Scripts"}) { slug name description viewerPermissions } }`
			},
			want: []string{`"slug":"tools","name":"tools","description":"Scripts"`, `"repo.manage"`}},
		{name: "a taken slug", viewer: olivia,
			document: func(f *orgFixture) string {
				return `mutation { createRepository(organizationId: "` + f.acme.ID.String() + `", input: {slug: "website"}) { slug } }`
			},
			wantCode: conflict, wantReason: "slug.taken"},
		{name: "an outsider", viewer: nia,
			document: func(f *orgFixture) string {
				return `mutation { createRepository(organizationId: "` + f.acme.ID.String() + `", input: {slug: "tools"}) { slug } }`
			},
			wantCode: notFound, wantReason: "organization.not_found"},
		{name: "a Maintainer renames it", viewer: olivia,
			document: func(f *orgFixture) string {
				return `mutation { updateRepository(id: "` + f.website.ID.String() + `", input: {slug: "www", name: "WWW", defaultBranch: "trunk"}) { slug name defaultBranch } }`
			},
			want: []string{`"slug":"www","name":"WWW","defaultBranch":"trunk"`}},
		{name: "a Member may not see it to rename it", viewer: maxim,
			document: func(f *orgFixture) string {
				return `mutation { updateRepository(id: "` + f.website.ID.String() + `", input: {slug: "www"}) { slug } }`
			},
			wantCode: notFound, wantReason: "repository.not_found"},
		{name: "grant a Member Reader", viewer: olivia,
			document: func(f *orgFixture) string {
				return `mutation { grantRepositoryRole(repositoryId: "` + f.website.ID.String() + `", principalId: "` + f.max.User.ID.String() + `", roleId: "` + auth.RoleReader.String() + `") }`
			},
			want: []string{`"grantRepositoryRole":true`}},
		{name: "grant someone outside", viewer: olivia,
			document: func(f *orgFixture) string {
				return `mutation { grantRepositoryRole(repositoryId: "` + f.website.ID.String() + `", principalId: "` + f.nia.User.ID.String() + `", roleId: "` + auth.RoleReader.String() + `") }`
			},
			wantCode: notFound, wantReason: "principal.not_found"},
		{name: "revoke the creator's Maintainer", viewer: olivia,
			document: func(f *orgFixture) string {
				return `mutation { revokeRepositoryRole(repositoryId: "` + f.website.ID.String() + `", principalId: "` + f.olivia.User.ID.String() + `", roleId: "` + auth.RoleMaintainer.String() + `") }`
			},
			want: []string{`"revokeRepositoryRole":true`}},
	})
}
