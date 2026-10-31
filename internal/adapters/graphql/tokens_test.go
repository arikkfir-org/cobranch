package graphql

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

func TestPersonalTokens(t *testing.T) {
	underToken := func(alice auth.Authenticated) *auth.Authenticated {
		return &auth.Authenticated{User: alice.User, Credential: auth.Credential{ID: uuid.New(), Kind: auth.CredentialPersonalToken, Permissions: []string{"repo.read"}}}
	}
	self := func(alice auth.Authenticated) *auth.Authenticated { return &alice }
	tests := []struct {
		name     string
		viewer   func(alice auth.Authenticated) *auth.Authenticated
		query    func(token uuid.UUID) string
		wantBody []string
	}{
		{name: "list as Alice", viewer: self, query: fixedQuery(`{ viewer { personalTokens { name hint permissions lastUsedAt } } }`),
			wantBody: []string{`"name":"Old laptop"`, `"permissions":["repo.push","repo.read"],"lastUsedAt":null`}},
		{name: "make one with the defaults", viewer: self, query: fixedQuery(`mutation { createPersonalToken(input: {name: "Laptop"}) { token { name permissions } secret } }`),
			wantBody: []string{`"name":"Laptop","permissions":["repo.push","repo.read"]`, `"secret":"cbp_`}},
		{name: "make one with permissions", viewer: self,
			query:    fixedQuery(`mutation { createPersonalToken(input: {name: "CI", permissions: ["repo.read", "org.read"]}) { token { permissions } } }`),
			wantBody: []string{`"permissions":["org.read","repo.read"]`}},
		{name: "a permission that isn't one", viewer: self, query: fixedQuery(`mutation { createPersonalToken(input: {name: "CI", permissions: ["repo.all"]}) { secret } }`),
			wantBody: []string{`"code":"BAD_USER_INPUT"`, `"reason":"token.permission_unknown"`}},
		{name: "make one under a token", viewer: underToken, query: fixedQuery(`mutation { createPersonalToken(input: {name: "Laptop"}) { secret } }`),
			wantBody: []string{`"code":"FORBIDDEN"`, `"reason":"token.session_required"`}},
		{name: "make one as anonymous", query: fixedQuery(`mutation { createPersonalToken(input: {name: "Laptop"}) { secret } }`),
			wantBody: []string{`"code":"UNAUTHENTICATED"`}},
		{name: "revoke one", viewer: self, query: func(token uuid.UUID) string { return `mutation { revokePersonalToken(id: "` + token.String() + `") }` },
			wantBody: []string{`{"data":{"revokePersonalToken":true}}`}},
		{name: "revoke one nobody has", viewer: self, query: fixedQuery(`mutation { revokePersonalToken(id: "` + uuid.NewString() + `") }`),
			wantBody: []string{`{"data":{"revokePersonalToken":false}}`}},
		{name: "revoke nonsense", viewer: self, query: fixedQuery(`mutation { revokePersonalToken(id: "nope") }`),
			wantBody: []string{`{"data":{"revokePersonalToken":false}}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, alice, _ := signedIn(t)
			token, _, err := service.CreateToken(context.Background(), alice, auth.TokenInput{Name: "Old laptop"})
			if err != nil {
				t.Fatal(err)
			}
			var viewer *auth.Authenticated
			if tt.viewer != nil {
				viewer = tt.viewer(alice)
			}
			req := httptest.NewRequest("POST", "/", strings.NewReader(`{"query":`+jsonString(tt.query(token.ID))+`}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			as(viewer, NewHandler("v1", service, emptyOrgs(), nil)).ServeHTTP(rec, req)
			for _, want := range tt.wantBody {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("body = %s, want %s in it", rec.Body, want)
				}
			}
		})
	}
}

// fixedQuery is a query that names no token.
func fixedQuery(query string) func(uuid.UUID) string { return func(uuid.UUID) string { return query } }
