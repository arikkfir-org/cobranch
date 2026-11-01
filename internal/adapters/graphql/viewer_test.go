package graphql

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/auth/authtest"
)

var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// signedIn returns sign-in's service over a store with Alice and two sessions of hers, and the viewer of the first.
func signedIn(t *testing.T) (*auth.Service, auth.Authenticated, auth.Session) {
	t.Helper()
	store := authtest.NewStore()
	ctx := context.Background()
	id := uuid.New()
	up := auth.SignUp{Principal: auth.Principal{ID: id, Kind: auth.KindUser, DisplayName: "Alice"},
		Identity: auth.Identity{Issuer: "https://idp.example.com", Subject: "1", UserID: id, Email: "alice@example.com", EmailVerified: true, LastSignInAt: now}}
	if err := store.SignUp(ctx, up); err != nil {
		t.Fatal(err)
	}
	sessions := make([]auth.Session, 2)
	for i := range sessions {
		sessions[i] = auth.Session{ID: uuid.New(), TokenHash: auth.Hash(string(rune('a' + i))), UserID: id, CreatedAt: now.Add(time.Duration(i) * time.Hour),
			LastSeenAt: now, IdleExpiresAt: now.Add(auth.SessionIdle), ExpiresAt: now.Add(auth.SessionMax), UserAgent: "browser", IP: "::1"}
		if err := store.SignIn(ctx, up.Identity, false, sessions[i]); err != nil {
			t.Fatal(err)
		}
	}
	service := auth.NewService(store, nil, auth.Settings{})
	service.SetClock(func() time.Time { return now.Add(time.Minute) })
	return service, auth.Authenticated{User: store.Users[id], Session: sessions[0]}, sessions[1]
}

// as serves h with viewer on every request's context, as the server's middleware would.
func as(viewer *auth.Authenticated, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if viewer != nil {
			r = r.WithContext(auth.WithViewer(r.Context(), *viewer))
		}
		h.ServeHTTP(w, r)
	})
}

func TestViewer(t *testing.T) {
	service, alice, other := signedIn(t)
	tests := []struct {
		name     string
		viewer   *auth.Authenticated
		query    string
		wantBody []string
		wantSess int
	}{
		{name: "anonymous", query: `{ viewer { id } }`, wantBody: []string{`{"data":{"viewer":null}}`}},
		{name: "Alice", viewer: &alice, query: `{ viewer { id displayName emails { email verified } sessions { id current userAgent } } }`,
			wantBody: []string{`"displayName":"Alice"`, `"email":"alice@example.com","verified":true`, `"id":"` + alice.Session.ID.String() + `","current":true`, `"id":"` + other.ID.String() + `","current":false`}},
		{name: "revoke another session", viewer: &alice, query: `mutation { revokeSession(id: "` + other.ID.String() + `") }`, wantBody: []string{`{"data":{"revokeSession":true}}`}},
		{name: "revoke it again", viewer: &alice, query: `mutation { revokeSession(id: "` + other.ID.String() + `") }`, wantBody: []string{`{"data":{"revokeSession":false}}`}},
		{name: "revoke nonsense", viewer: &alice, query: `mutation { revokeSession(id: "nope") }`, wantBody: []string{`{"data":{"revokeSession":false}}`}},
		{name: "revoke as anonymous", query: `mutation { revokeSession(id: "` + other.ID.String() + `") }`, wantBody: []string{`"code":"UNAUTHENTICATED"`, `"data":null`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"query":` + jsonString(tt.query) + `}`
			req := httptest.NewRequest("POST", "/", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			as(tt.viewer, NewHandler("v1", service, emptyOrgs(), nil, nil)).ServeHTTP(rec, req)
			for _, want := range tt.wantBody {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("body = %s, want %s in it", rec.Body, want)
				}
			}
		})
	}
}

// jsonString quotes s as a JSON string.
func jsonString(s string) string {
	return `"` + strings.NewReplacer(`"`, `\"`).Replace(s) + `"`
}
