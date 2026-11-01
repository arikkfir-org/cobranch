package rest

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

func TestMyTokens(t *testing.T) {
	underToken := func(alice auth.Authenticated) *auth.Authenticated {
		return &auth.Authenticated{User: alice.User, Credential: auth.Credential{ID: uuid.New(), Kind: auth.CredentialPersonalToken, Permissions: []string{"repo.read"}}}
	}
	runTokenCases(t, []tokenCase{
		{name: "list as anonymous", method: "GET", path: fixedPath("/v1/me/tokens"), wantStatus: 401, wantCode: "auth.unauthenticated"},
		{name: "list as Alice", viewer: self, method: "GET", path: fixedPath("/v1/me/tokens"), wantStatus: 200, check: checkTokens},
		{name: "make one as anonymous", method: "POST", path: fixedPath("/v1/me/tokens"), body: `{"name":"Laptop"}`, wantStatus: 401, wantCode: "auth.unauthenticated"},
		{name: "make one with the defaults", viewer: self, method: "POST", path: fixedPath("/v1/me/tokens"), body: `{"name":"Laptop"}`, wantStatus: 201,
			check: checkCreated([]string{"repo.push", "repo.read"}, now.Add(time.Minute).Add(auth.TokenLifetime))},
		{name: "make one with permissions and an end", viewer: self, method: "POST", path: fixedPath("/v1/me/tokens"),
			body: `{"name":"CI","permissions":["repo.read","org.read"],"expiresAt":"2026-11-06T12:00:00Z"}`, wantStatus: 201,
			check: checkCreated([]string{"org.read", "repo.read"}, time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC))},
		{name: "a permission that isn't one", viewer: self, method: "POST", path: fixedPath("/v1/me/tokens"), body: `{"name":"CI","permissions":["repo.all"]}`,
			wantStatus: 400, wantCode: "token.permission_unknown"},
		{name: "an end past a year", viewer: self, method: "POST", path: fixedPath("/v1/me/tokens"), body: `{"name":"CI","expiresAt":"2028-01-01T00:00:00Z"}`,
			wantStatus: 400, wantCode: "token.expiry_invalid"},
		{name: "make one under a token", viewer: underToken, method: "POST", path: fixedPath("/v1/me/tokens"), body: `{"name":"Laptop"}`,
			wantStatus: 403, wantCode: "token.session_required"},
		{name: "revoke as anonymous", method: "DELETE", path: tokenPath, wantStatus: 401, wantCode: "auth.unauthenticated"},
		{name: "revoke one", viewer: self, method: "DELETE", path: tokenPath, wantStatus: 204,
			check: func(t *testing.T, service *auth.Service, alice auth.Authenticated, _ []byte) {
				if tokens, err := service.Tokens(context.Background(), alice.User.ID); err != nil || len(tokens) != 0 {
					t.Errorf("Tokens() after the revocation = %+v, %v, want none", tokens, err)
				}
			}},
		{name: "revoke one nobody has", viewer: self, method: "DELETE", path: fixedPath("/v1/me/tokens/" + uuid.NewString()), wantStatus: 404, wantCode: "token.not_found"},
		{name: "revoke nonsense", viewer: self, method: "DELETE", path: fixedPath("/v1/me/tokens/nope"), wantStatus: 400, wantCode: "request.invalid"},
	})
}

// tokenCase is a request to the viewer's personal tokens or SSH keys, against Alice with one of hers, "Old laptop",
// already made.
type tokenCase struct {
	name       string
	viewer     func(alice auth.Authenticated) *auth.Authenticated
	method     string
	path       func(token uuid.UUID) string
	body       string
	wantStatus int
	wantCode   string
	check      func(t *testing.T, service *auth.Service, alice auth.Authenticated, body []byte)
}

func runTokenCases(t *testing.T, tests []tokenCase) {
	t.Helper()
	runCredentialCases(t, func(t *testing.T, service *auth.Service, alice auth.Authenticated) uuid.UUID {
		t.Helper()
		token, _, err := service.CreateToken(context.Background(), alice, auth.TokenInput{Name: "Old laptop"})
		if err != nil {
			t.Fatal(err)
		}
		return token.ID
	}, tests)
}

// runCredentialCases runs tests, each after seed made Alice's credential "Old laptop".
func runCredentialCases(t *testing.T, seed func(t *testing.T, service *auth.Service, alice auth.Authenticated) uuid.UUID, tests []tokenCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, alice, _ := signedIn(t)
			id := seed(t, service, alice)
			var viewer *auth.Authenticated
			if tt.viewer != nil {
				viewer = tt.viewer(alice)
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path(id), strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			as(viewer, NewHandler("v1", service, emptyOrgs(t), nil, nil)).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantCode != "" {
				checkBody(t, rec, "", tt.wantCode)
			}
			if tt.check != nil {
				tt.check(t, service, alice, rec.Body.Bytes())
			}
		})
	}
}

func self(alice auth.Authenticated) *auth.Authenticated { return &alice }

// fixedPath is a path that names no token.
func fixedPath(path string) func(uuid.UUID) string { return func(uuid.UUID) string { return path } }

// tokenPath is the path of the token the case starts with.
func tokenPath(token uuid.UUID) string { return "/v1/me/tokens/" + token.String() }

// checkTokens checks that body lists the token the case starts with, as its user sees it.
func checkTokens(t *testing.T, _ *auth.Service, _ auth.Authenticated, body []byte) {
	t.Helper()
	got := decode[struct{ Tokens []PersonalToken }](t, body)
	if len(got.Tokens) != 1 || got.Tokens[0].Name != "Old laptop" || len(got.Tokens[0].Hint) != 4 || !slices.Equal(got.Tokens[0].Permissions, auth.DefaultTokenPermissions) ||
		got.Tokens[0].LastUsedAt != nil {
		t.Errorf("tokens = %s", body)
	}
}

// checkCreated checks that body is a new token with permissions, ending at expiresAt, whose secret signs Alice in.
func checkCreated(permissions []string, expiresAt time.Time) func(t *testing.T, service *auth.Service, alice auth.Authenticated, body []byte) {
	return func(t *testing.T, service *auth.Service, alice auth.Authenticated, body []byte) {
		t.Helper()
		got := decode[PersonalTokenCreated](t, body)
		if !slices.Equal(got.Token.Permissions, permissions) || !got.Token.ExpiresAt.Equal(expiresAt) || !strings.HasSuffix(got.Secret, got.Token.Hint) {
			t.Errorf("created = %s, want %v ending %v", body, permissions, expiresAt)
		}
		a, err := service.Authenticate(context.Background(), got.Secret)
		if err != nil || a.User.ID != alice.User.ID || a.Credential.ID != got.Token.Id {
			t.Errorf("Authenticate(the secret) = %+v, %v, want Alice under the token", a, err)
		}
	}
}
