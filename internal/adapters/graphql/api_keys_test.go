package graphql

import (
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// withKey adds Acme's key "Deploy", holding Member with one secret, to the fixture, and returns it.
func (f *orgFixture) withKey() orgs.APIKey {
	key := orgs.APIKey{ID: uuid.New(), OrgID: f.acme.ID, Name: "Deploy", CreatedBy: f.olivia.User.ID, CreatedAt: now}
	return f.store.AddKey(key, auth.RoleMember)
}

func TestAPIKeyMutations(t *testing.T) {
	member := auth.RoleMember.String()
	runOrgCases(t, []orgCase{
		{name: "make a key", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { createApiKey(organizationId: "` + f.acme.ID.String() + `", input: {name: "Deploy", roleIds: ["` + member + `"]}) { apiKey { name createdBy roles { name } secrets { hint expiresAt } } secret } }`
		}, want: []string{`"name":"Deploy","createdBy":"`, `"roles":[{"name":"Member"}]`, `"secret":"cbk_`}},
		{name: "make a key that never expires", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { createApiKey(organizationId: "` + f.acme.ID.String() + `", input: {name: "Deploy", roleIds: ["` + member + `"], neverExpires: true}) { apiKey { secrets { expiresAt } } } }`
		}, want: []string{`"secrets":[{"expiresAt":null}]`}},
		{name: "make a key as a Member", viewer: maxim, document: func(f *orgFixture) string {
			return `mutation { createApiKey(organizationId: "` + f.acme.ID.String() + `", input: {name: "Deploy", roleIds: ["` + member + `"]}) { secret } }`
		}, wantCode: forbidden},
		{name: "make a key without roles", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { createApiKey(organizationId: "` + f.acme.ID.String() + `", input: {name: "Deploy", roleIds: []}) { secret } }`
		}, wantCode: badUserInput, wantReason: "api_key.roles_invalid"},
		{name: "make a key with nonsense roles", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { createApiKey(organizationId: "` + f.acme.ID.String() + `", input: {name: "Deploy", roleIds: ["x"]}) { secret } }`
		}, wantCode: notFound, wantReason: "role.not_found"},
		{name: "list keys", viewer: maxim, document: func(f *orgFixture) string {
			f.withKey()
			return `{ organization(slug: "acme") { apiKeys { name roles { name } secrets { hint revokedAt } } } }`
		}, want: []string{`"apiKeys":[{"name":"Deploy","roles":[{"name":"Member"}],"secrets":[{"hint":"zzzz","revokedAt":null}]}]`}},
		{name: "rotate", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { rotateApiKey(organizationId: "` + f.acme.ID.String() + `", id: "` + f.withKey().ID.String() + `") { apiKey { secrets { hint } } secret } }`
		}, want: []string{`"secrets":[{"hint":"zzzz"},{"hint":"`, `"secret":"cbk_`}},
		{name: "rotate with an end that passed", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { rotateApiKey(organizationId: "` + f.acme.ID.String() + `", id: "` + f.withKey().ID.String() + `", input: {expiresAt: "2020-01-01T00:00:00Z"}) { secret } }`
		}, wantCode: badUserInput, wantReason: "api_key.expiry_invalid"},
		{name: "rotate a key that isn't", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { rotateApiKey(organizationId: "` + f.acme.ID.String() + `", id: "` + uuid.New().String() + `") { secret } }`
		}, wantCode: notFound, wantReason: "api_key.not_found"},
		{name: "revoke a secret", viewer: olivia, document: func(f *orgFixture) string {
			key := f.withKey()
			return `mutation { revokeApiKeySecret(organizationId: "` + f.acme.ID.String() + `", id: "` + key.ID.String() + `", secretId: "` + key.Secrets[0].ID.String() + `") }`
		}, want: []string{`{"data":{"revokeApiKeySecret":true}}`}},
		{name: "revoke a secret that isn't", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { revokeApiKeySecret(organizationId: "` + f.acme.ID.String() + `", id: "` + f.withKey().ID.String() + `", secretId: "` + uuid.New().String() + `") }`
		}, wantCode: notFound, wantReason: "api_key.not_found"},
		{name: "delete a key", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { deleteApiKey(organizationId: "` + f.acme.ID.String() + `", id: "` + f.withKey().ID.String() + `") }`
		}, want: []string{`{"data":{"deleteApiKey":true}}`}},
		{name: "delete a key as a Member", viewer: maxim, document: func(f *orgFixture) string {
			return `mutation { deleteApiKey(organizationId: "` + f.acme.ID.String() + `", id: "` + f.withKey().ID.String() + `") }`
		}, wantCode: forbidden},
	})
}
