package rest

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// withKey adds Acme's key "Deploy", holding Member with one secret, to the fixture, and returns it.
func (f *orgFixture) withKey(secrets ...orgs.Secret) orgs.APIKey {
	key := orgs.APIKey{ID: uuid.New(), OrgID: f.acme.ID, Name: "Deploy", CreatedBy: f.olivia.User.ID, CreatedAt: now, Secrets: secrets}
	return f.store.AddKey(key, auth.RoleMember)
}

// asKey is the fixture's key as a viewer: an API key's principal, which a bearer token resolved to.
func asKey(f *orgFixture) *auth.Authenticated {
	key := f.withKey()
	return &auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: key.ID, Kind: auth.KindAPIKey, DisplayName: key.Name}}}
}

func TestAPIKeysAPI(t *testing.T) {
	member := auth.RoleMember.String()
	keyPath := func(f *orgFixture) string { return "/v1/orgs/acme/api-keys/" + f.withKey().ID.String() }
	runOrgCases(t, []orgCase{
		{name: "make a key", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/api-keys"), body: `{"name":"Deploy","roleIds":["` + member + `"]}`, wantStatus: 201,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				got := decode[ApiKeyCreated](t, body)
				if got.ApiKey.Name != "Deploy" || got.ApiKey.OrgId != f.acme.ID || got.ApiKey.CreatedBy == nil || len(got.ApiKey.Roles) != 1 || got.ApiKey.Roles[0].Name != "Member" ||
					len(got.ApiKey.Secrets) != 1 || got.ApiKey.Secrets[0].ExpiresAt == nil || !strings.HasPrefix(got.Secret, "cbk_") || got.ApiKey.Secrets[0].Hint != got.Secret[len(got.Secret)-4:] {
					t.Errorf("created = %s", body)
				}
			}},
		{name: "make a key that never expires", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/api-keys"), body: `{"name":"Deploy","roleIds":["` + member + `"],"neverExpires":true}`, wantStatus: 201,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[ApiKeyCreated](t, body); got.ApiKey.Secrets[0].ExpiresAt != nil {
					t.Errorf("created = %s, want no end", body)
				}
			}},
		{name: "make a key as a Member", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/api-keys"), body: `{"name":"Deploy","roleIds":["` + member + `"]}`, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "make a key without roles", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/api-keys"), body: `{"name":"Deploy","roleIds":[]}`, wantStatus: 400, wantCode: "api_key.roles_invalid"},
		{name: "make a key with a role that isn't", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/api-keys"), body: `{"name":"Deploy","roleIds":["00000000-0000-7000-8000-00000000ffff"]}`, wantStatus: 400, wantCode: "role.invalid"},
		{name: "make a key with an end that passed", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/api-keys"), body: `{"name":"Deploy","roleIds":["` + member + `"],"expiresAt":"2020-01-01T00:00:00Z"}`, wantStatus: 400, wantCode: "api_key.expiry_invalid"},
		{name: "list keys", viewer: maxim, method: "GET", path: func(f *orgFixture) string { f.withKey(); return "/v1/orgs/acme/api-keys" }, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ APIKeys []ApiKey }](t, body); len(got.APIKeys) != 1 || got.APIKeys[0].Name != "Deploy" || len(got.APIKeys[0].Secrets) != 1 || got.APIKeys[0].Secrets[0].Hint != "zzzz" {
					t.Errorf("keys = %s", body)
				}
			}},
		{name: "list keys from outside", viewer: nia, method: "GET", path: fixed("/v1/orgs/acme/api-keys"), wantStatus: 404, wantCode: "organization.not_found"},
		{name: "rotate", viewer: olivia, method: "POST", path: func(f *orgFixture) string { return keyPath(f) + "/secrets" }, body: `{"neverExpires":true}`, wantStatus: 201,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[ApiKeyCreated](t, body); len(got.ApiKey.Secrets) != 2 || got.ApiKey.Secrets[1].ExpiresAt != nil || !strings.HasPrefix(got.Secret, "cbk_") {
					t.Errorf("rotated = %s", body)
				}
			}},
		{name: "rotate with no body", viewer: olivia, method: "POST", path: func(f *orgFixture) string { return keyPath(f) + "/secrets" }, wantStatus: 201},
		{name: "rotate a key with two secrets", viewer: olivia, method: "POST", path: func(f *orgFixture) string {
			key := f.withKey(orgs.Secret{ID: uuid.New(), Hint: "aaaa", CreatedAt: now}, orgs.Secret{ID: uuid.New(), Hint: "bbbb", CreatedAt: now})
			return "/v1/orgs/acme/api-keys/" + key.ID.String() + "/secrets"
		}, wantStatus: 409, wantCode: "api_key.two_secrets"},
		{name: "rotate as a Member", viewer: maxim, method: "POST", path: func(f *orgFixture) string { return keyPath(f) + "/secrets" }, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "rotate a key that isn't", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/api-keys/00000000-0000-7000-8000-00000000ffff/secrets"), wantStatus: 404, wantCode: "api_key.not_found"},
		{name: "revoke a secret", viewer: olivia, method: "DELETE", path: func(f *orgFixture) string {
			key := f.withKey()
			return "/v1/orgs/acme/api-keys/" + key.ID.String() + "/secrets/" + key.Secrets[0].ID.String()
		}, wantStatus: 204},
		{name: "revoke a secret that isn't", viewer: olivia, method: "DELETE", path: func(f *orgFixture) string { return keyPath(f) + "/secrets/00000000-0000-7000-8000-00000000ffff" }, wantStatus: 404, wantCode: "secret.not_found"},
		{name: "delete a key", viewer: olivia, method: "DELETE", path: keyPath, wantStatus: 204},
		{name: "delete a key as a Member", viewer: maxim, method: "DELETE", path: keyPath, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "delete a key that isn't", viewer: olivia, method: "DELETE", path: fixed("/v1/orgs/acme/api-keys/00000000-0000-7000-8000-00000000ffff"), wantStatus: 404, wantCode: "api_key.not_found"},
	})
}

// TestAPIKeyViewer checks what a request with a key's secret may do.
func TestAPIKeyViewer(t *testing.T) {
	member := auth.RoleMember.String()
	runOrgCases(t, []orgCase{
		{name: "a key reads its organization", viewer: asKey, method: "GET", path: fixed("/v1/orgs/acme"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[Organization](t, body); got.Slug != "acme" || !contains(got.ViewerPermissions, "org.read") {
					t.Errorf("organization = %s", body)
				}
			}},
		{name: "a key may not make keys", viewer: asKey, method: "POST", path: fixed("/v1/orgs/acme/api-keys"), body: `{"name":"Other","roleIds":["` + member + `"]}`, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "a key has no sessions", viewer: asKey, method: "GET", path: fixed("/v1/me"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[User](t, body); got.DisplayName != "Deploy" || len(got.Emails) != 0 {
					t.Errorf("me = %s", body)
				}
			}},
	})
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestSecretTimes keeps a secret's times as the API shows them: absent when unset.
func TestSecretTimes(t *testing.T) {
	at := now.Add(time.Hour)
	got := apiKeySecret(orgs.Secret{Hint: "abcd", CreatedAt: now, ExpiresAt: at})
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(at) || got.LastUsedAt != nil || got.RevokedAt != nil {
		t.Errorf("apiKeySecret() = %+v", got)
	}
}
