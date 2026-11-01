package rest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/auth/authtest"
)

func TestMySSHKeys(t *testing.T) {
	underToken := func(alice auth.Authenticated) *auth.Authenticated {
		return &auth.Authenticated{User: alice.User, Credential: auth.Credential{ID: uuid.New(), Kind: auth.CredentialPersonalToken, Permissions: []string{"repo.read"}}}
	}
	old, key := authtest.NewSSHKey(t).PublicKey(), authtest.NewSSHKey(t).PublicKey()
	added := sshKeyBody("", authtest.AuthorizedKey(key, "alice@desktop"))
	runCredentialCases(t, seedSSHKey(old), []tokenCase{
		{name: "list as anonymous", method: "GET", path: fixedPath("/v1/me/ssh-keys"), wantStatus: 401, wantCode: "auth.unauthenticated"},
		{name: "list as Alice", viewer: self, method: "GET", path: fixedPath("/v1/me/ssh-keys"), wantStatus: 200, check: checkSSHKeys(old)},
		{name: "add one as anonymous", method: "POST", path: fixedPath("/v1/me/ssh-keys"), body: added, wantStatus: 401, wantCode: "auth.unauthenticated"},
		{name: "add one named by its comment", viewer: self, method: "POST", path: fixedPath("/v1/me/ssh-keys"), body: added, wantStatus: 201,
			check: checkAdded(key, "alice@desktop")},
		{name: "add one with a name", viewer: self, method: "POST", path: fixedPath("/v1/me/ssh-keys"), body: sshKeyBody("Desktop", authtest.AuthorizedKey(key, "")),
			wantStatus: 201, check: checkAdded(key, "Desktop")},
		{name: "a key that isn't one", viewer: self, method: "POST", path: fixedPath("/v1/me/ssh-keys"), body: sshKeyBody("Desktop", "ssh-ed25519 nope"),
			wantStatus: 400, wantCode: "ssh_key.invalid"},
		{name: "a key added already", viewer: self, method: "POST", path: fixedPath("/v1/me/ssh-keys"), body: sshKeyBody("Again", authtest.AuthorizedKey(old, "")),
			wantStatus: 400, wantCode: "ssh_key.taken"},
		{name: "add one under a token", viewer: underToken, method: "POST", path: fixedPath("/v1/me/ssh-keys"), body: added,
			wantStatus: 403, wantCode: "ssh_key.session_required"},
		{name: "delete as anonymous", method: "DELETE", path: sshKeyPath, wantStatus: 401, wantCode: "auth.unauthenticated"},
		{name: "delete one", viewer: self, method: "DELETE", path: sshKeyPath, wantStatus: 204,
			check: func(t *testing.T, service *auth.Service, alice auth.Authenticated, _ []byte) {
				if keys, err := service.SSHKeys(context.Background(), alice.User.ID); err != nil || len(keys) != 0 {
					t.Errorf("SSHKeys() after the deletion = %+v, %v, want none", keys, err)
				}
			}},
		{name: "delete one nobody has", viewer: self, method: "DELETE", path: fixedPath("/v1/me/ssh-keys/" + uuid.NewString()), wantStatus: 404, wantCode: "ssh_key.not_found"},
		{name: "delete one under a token", viewer: underToken, method: "DELETE", path: sshKeyPath, wantStatus: 403, wantCode: "ssh_key.session_required",
			check: func(t *testing.T, service *auth.Service, alice auth.Authenticated, _ []byte) {
				if keys, err := service.SSHKeys(context.Background(), alice.User.ID); err != nil || len(keys) != 1 {
					t.Errorf("SSHKeys() after the refusal = %+v, %v, want the key kept", keys, err)
				}
			}},
	})
}

// seedSSHKey adds key as Alice's SSH key "Old laptop".
func seedSSHKey(key ssh.PublicKey) func(t *testing.T, service *auth.Service, alice auth.Authenticated) uuid.UUID {
	return func(t *testing.T, service *auth.Service, alice auth.Authenticated) uuid.UUID {
		t.Helper()
		added, err := service.AddSSHKey(context.Background(), alice, auth.SSHKeyInput{Name: "Old laptop", PublicKey: authtest.AuthorizedKey(key, "")})
		if err != nil {
			t.Fatal(err)
		}
		return added.ID
	}
}

// sshKeyPath is the path of the key the case starts with.
func sshKeyPath(key uuid.UUID) string { return "/v1/me/ssh-keys/" + key.String() }

// sshKeyBody is a request body that adds publicKey named name, or by its comment when name is empty.
func sshKeyBody(name, publicKey string) string {
	in := map[string]string{"publicKey": publicKey}
	if name != "" {
		in["name"] = name
	}
	body, _ := json.Marshal(in) // a map of strings always marshals
	return string(body)
}

// checkSSHKeys checks that body lists key, the one the case starts with, as its user sees it.
func checkSSHKeys(key ssh.PublicKey) func(t *testing.T, service *auth.Service, alice auth.Authenticated, body []byte) {
	return func(t *testing.T, _ *auth.Service, _ auth.Authenticated, body []byte) {
		t.Helper()
		got := decode[struct {
			SSHKeys []SSHKey `json:"sshKeys"`
		}](t, body)
		if len(got.SSHKeys) != 1 || got.SSHKeys[0].Name != "Old laptop" || got.SSHKeys[0].PublicKey != authtest.AuthorizedKey(key, "") ||
			got.SSHKeys[0].Fingerprint != ssh.FingerprintSHA256(key) || got.SSHKeys[0].LastUsedAt != nil {
			t.Errorf("keys = %s", body)
		}
	}
}

// checkAdded checks that body is key, added as name, which then signs Alice in.
func checkAdded(key ssh.PublicKey, name string) func(t *testing.T, service *auth.Service, alice auth.Authenticated, body []byte) {
	return func(t *testing.T, service *auth.Service, alice auth.Authenticated, body []byte) {
		t.Helper()
		got := decode[SSHKey](t, body)
		if got.Name != name || got.Fingerprint != ssh.FingerprintSHA256(key) || got.PublicKey != authtest.AuthorizedKey(key, "") ||
			strings.Contains(got.PublicKey, "alice@") {
			t.Errorf("added = %s, want %s named %q", body, ssh.FingerprintSHA256(key), name)
		}
		a, err := service.AuthenticateSSHKey(context.Background(), key.Marshal())
		if err != nil || a.User.ID != alice.User.ID || a.Credential.ID != got.Id {
			t.Errorf("AuthenticateSSHKey(the key) = %+v, %v, want Alice under the key", a, err)
		}
	}
}
