package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

// fakeAuthenticator signs the token "cbs_alice" in as Alice, fails "cbs_down", and refuses the rest.
type fakeAuthenticator struct{}

func (fakeAuthenticator) Authenticate(_ context.Context, token string) (auth.Authenticated, error) {
	switch token {
	case "cbs_alice":
		return auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: uuid.MustParse("00000000-0000-7000-8000-00000000aaaa"), DisplayName: "Alice"}}}, nil
	case "cbs_down":
		return auth.Authenticated{}, errors.New("down")
	default:
		return auth.Authenticated{}, auth.ErrUnauthenticated
	}
}

func TestAuthenticate(t *testing.T) {
	const console = "https://cobranch.example.com"
	tests := []struct {
		name        string
		method      string
		token       string
		origin      string
		contentType string
		body        string
		wantStatus  int
		wantViewer  string
	}{
		{name: "no cookie", method: "GET", wantStatus: 200, wantViewer: "anonymous"},
		{name: "a live session", method: "GET", token: "cbs_alice", wantStatus: 200, wantViewer: "Alice"},
		{name: "a dead session", method: "GET", token: "cbs_old", wantStatus: 401},
		{name: "a check that fails", method: "GET", token: "cbs_down", wantStatus: 500},
		{name: "a JSON mutation from the Console", method: "POST", token: "cbs_alice", origin: console, contentType: "application/json", body: "{}", wantStatus: 200, wantViewer: "Alice"},
		{name: "a JSON mutation with no Origin", method: "POST", token: "cbs_alice", contentType: "application/json; charset=utf-8", body: "{}", wantStatus: 200, wantViewer: "Alice"},
		{name: "a mutation from another origin", method: "POST", token: "cbs_alice", origin: "https://evil.example.com", contentType: "application/json", body: "{}", wantStatus: 403},
		{name: "a form from the Console", method: "POST", token: "cbs_alice", origin: console, contentType: "application/x-www-form-urlencoded", body: "a=b", wantStatus: 403},
		{name: "a form with no Origin", method: "POST", token: "cbs_alice", contentType: "text/plain", body: "{}", wantStatus: 403},
		{name: "a DELETE with no body from the Console", method: "DELETE", token: "cbs_alice", origin: console, wantStatus: 200, wantViewer: "Alice"},
		{name: "a mutation without the cookie", method: "POST", origin: "https://evil.example.com", contentType: "text/plain", body: "x", wantStatus: 200, wantViewer: "anonymous"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := Authenticate(auth.HostCookie, fakeAuthenticator{}, console, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if v, ok := auth.Viewer(r.Context()); ok {
					_, _ = io.WriteString(w, v.User.DisplayName)
					return
				}
				_, _ = io.WriteString(w, "anonymous")
			}))
			req := cookieRequest(tt.method, tt.body, tt.token, tt.origin, tt.contentType)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantViewer != "" && rec.Body.String() != tt.wantViewer {
				t.Errorf("viewer = %q, want %q", rec.Body, tt.wantViewer)
			}
		})
	}
}

// cookieRequest is a request for /v1/me with body, the session cookie token, and the Origin and Content-Type headers
// that are not empty.
func cookieRequest(method, body, token, origin, contentType string) *http.Request {
	req := httptest.NewRequest(method, "/v1/me", strings.NewReader(body))
	if body == "" {
		req.ContentLength = 0
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: auth.HostCookie, Value: token})
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req
}
