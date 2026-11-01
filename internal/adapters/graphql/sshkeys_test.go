package graphql

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/auth/authtest"
)

func TestSSHKeys(t *testing.T) {
	underToken := func(alice auth.Authenticated) *auth.Authenticated {
		return &auth.Authenticated{User: alice.User, Credential: auth.Credential{ID: uuid.New(), Kind: auth.CredentialPersonalToken, Permissions: []string{"repo.read"}}}
	}
	self := func(alice auth.Authenticated) *auth.Authenticated { return &alice }
	old, key := authtest.NewSSHKey(t).PublicKey(), authtest.NewSSHKey(t).PublicKey()
	add := func(name, publicKey string) func(uuid.UUID) string {
		return fixedQuery(`mutation { addSSHKey(input: {name: ` + jsonString(name) + `, publicKey: ` + jsonString(publicKey) + `}) { name publicKey fingerprint } }`)
	}
	tests := []struct {
		name     string
		viewer   func(alice auth.Authenticated) *auth.Authenticated
		query    func(key uuid.UUID) string
		wantBody []string
	}{
		{name: "list as Alice", viewer: self, query: fixedQuery(`{ viewer { sshKeys { name publicKey fingerprint lastUsedAt } } }`),
			wantBody: []string{`"name":"Old laptop","publicKey":"` + authtest.AuthorizedKey(old, "") + `","fingerprint":"` + ssh.FingerprintSHA256(old) + `","lastUsedAt":null`}},
		{name: "add one named by its comment", viewer: self, query: add("", authtest.AuthorizedKey(key, "alice@desktop")),
			wantBody: []string{`"name":"alice@desktop","publicKey":"` + authtest.AuthorizedKey(key, "") + `","fingerprint":"` + ssh.FingerprintSHA256(key) + `"`}},
		{name: "add one with a name", viewer: self, query: add("Desktop", authtest.AuthorizedKey(key, "alice@desktop")), wantBody: []string{`"name":"Desktop"`}},
		{name: "a key added already", viewer: self, query: add("Again", authtest.AuthorizedKey(old, "")),
			wantBody: []string{`"code":"BAD_USER_INPUT"`, `"reason":"ssh_key.taken"`}},
		{name: "a key that isn't one", viewer: self, query: add("Desktop", "ssh-ed25519 nope"), wantBody: []string{`"code":"BAD_USER_INPUT"`, `"reason":"ssh_key.invalid"`}},
		{name: "add one under a token", viewer: underToken, query: add("Desktop", authtest.AuthorizedKey(key, "")),
			wantBody: []string{`"code":"FORBIDDEN"`, `"reason":"ssh_key.session_required"`}},
		{name: "add one as anonymous", query: add("Desktop", authtest.AuthorizedKey(key, "")), wantBody: []string{`"code":"UNAUTHENTICATED"`}},
		{name: "delete one", viewer: self, query: func(key uuid.UUID) string { return `mutation { deleteSSHKey(id: "` + key.String() + `") }` },
			wantBody: []string{`{"data":{"deleteSSHKey":true}}`}},
		{name: "delete one under a token", viewer: underToken, query: func(key uuid.UUID) string { return `mutation { deleteSSHKey(id: "` + key.String() + `") }` },
			wantBody: []string{`"code":"FORBIDDEN"`, `"reason":"ssh_key.session_required"`}},
		{name: "delete one nobody has", viewer: self, query: fixedQuery(`mutation { deleteSSHKey(id: "` + uuid.NewString() + `") }`),
			wantBody: []string{`{"data":{"deleteSSHKey":false}}`}},
		{name: "delete nonsense", viewer: self, query: fixedQuery(`mutation { deleteSSHKey(id: "nope") }`), wantBody: []string{`{"data":{"deleteSSHKey":false}}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, alice, _ := signedIn(t)
			seeded, err := service.AddSSHKey(context.Background(), alice, auth.SSHKeyInput{Name: "Old laptop", PublicKey: authtest.AuthorizedKey(old, "")})
			if err != nil {
				t.Fatal(err)
			}
			var viewer *auth.Authenticated
			if tt.viewer != nil {
				viewer = tt.viewer(alice)
			}
			req := httptest.NewRequest("POST", "/", strings.NewReader(`{"query":`+jsonString(tt.query(seeded.ID))+`}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			as(viewer, NewHandler("v1", service, emptyOrgs(), nil, nil)).ServeHTTP(rec, req)
			for _, want := range tt.wantBody {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("body = %s, want %s in it", rec.Body, want)
				}
			}
		})
	}
}
