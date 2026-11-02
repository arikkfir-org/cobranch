package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestMe(t *testing.T) {
	service, alice, other := signedIn(t)
	tests := []struct {
		name       string
		viewer     *auth.Authenticated
		method     string
		path       string
		wantStatus int
		wantCode   string
		check      func(t *testing.T, body []byte)
	}{
		{name: "me as anonymous", method: "GET", path: "/v1/me", wantStatus: 401, wantCode: "auth.unauthenticated"},
		{name: "me as Alice", viewer: &alice, method: "GET", path: "/v1/me", wantStatus: 200, check: checkMe(alice)},
		{name: "sessions as anonymous", method: "GET", path: "/v1/me/sessions", wantStatus: 401, wantCode: "auth.unauthenticated"},
		{name: "sessions as Alice", viewer: &alice, method: "GET", path: "/v1/me/sessions", wantStatus: 200, check: checkSessions(alice)},
		{name: "revoke as anonymous", method: "DELETE", path: "/v1/me/sessions/" + other.ID.String(), wantStatus: 401, wantCode: "auth.unauthenticated"},
		{name: "revoke another session", viewer: &alice, method: "DELETE", path: "/v1/me/sessions/" + other.ID.String(), wantStatus: 204},
		{name: "revoke it again", viewer: &alice, method: "DELETE", path: "/v1/me/sessions/" + other.ID.String(), wantStatus: 404, wantCode: "session.not_found"},
		{name: "revoke nonsense", viewer: &alice, method: "DELETE", path: "/v1/me/sessions/nope", wantStatus: 400, wantCode: "request.invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			as(tt.viewer, NewHandler("v1", service, emptyOrgs(t), nil, nil, nil)).ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantCode != "" {
				checkBody(t, rec, "", tt.wantCode)
			}
			if tt.check != nil {
				tt.check(t, rec.Body.Bytes())
			}
		})
	}
}

// checkMe checks that body is alice as the API shows her.
func checkMe(alice auth.Authenticated) func(t *testing.T, body []byte) {
	return func(t *testing.T, body []byte) {
		t.Helper()
		var u User
		if err := json.Unmarshal(body, &u); err != nil || u.Id != alice.User.ID || u.DisplayName != "Alice" || len(u.Emails) != 1 || u.Emails[0].Email != "alice@example.com" {
			t.Errorf("me = %s, %v", body, err)
		}
	}
}

// checkSessions checks that body lists alice's two sessions, hers marked current, ending when idle.
func checkSessions(alice auth.Authenticated) func(t *testing.T, body []byte) {
	return func(t *testing.T, body []byte) {
		t.Helper()
		var got struct{ Sessions []Session }
		if err := json.Unmarshal(body, &got); err != nil || len(got.Sessions) != 2 {
			t.Fatalf("sessions = %s, %v", body, err)
		}
		for _, s := range got.Sessions {
			if s.Current != (s.Id == alice.Session.ID) || !s.ExpiresAt.Equal(now.Add(auth.SessionIdle)) {
				t.Errorf("session = %+v", s)
			}
		}
	}
}
