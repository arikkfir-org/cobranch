package authserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/auth/authtest"
)

var alice = auth.Claims{Issuer: "https://idp.example.com", Subject: "1", Email: "alice@example.com", EmailVerified: true, Name: "Alice"}

const console = "https://cobranch.example.com"

// newHandler serves the Auth server over a fake store and provider, under settings, with cookie.
func newHandler(t *testing.T, settings auth.Settings, cookie auth.Cookie) (http.Handler, *authtest.Store) {
	t.Helper()
	store := authtest.NewStore()
	service := auth.NewService(store, []auth.Provider{&authtest.Provider{ProviderID: "google", Claims: alice}}, settings)
	u, err := url.Parse(console)
	if err != nil {
		t.Fatal(err)
	}
	return Handler(service, cookie, u), store
}

// cookieNamed finds the cookie name in rec's response, or nil.
func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestProviders(t *testing.T) {
	demo := auth.SignUp{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: "Demo Owner"},
		Identity: auth.Identity{Issuer: auth.SeedIssuer, Subject: "owner", Email: "owner@example.com", EmailVerified: true}}
	demo.Identity.UserID = demo.Principal.ID
	tests := []struct {
		name       string
		seed       bool
		origin     string
		wantSeeded int
		wantOrigin string
	}{
		{name: "providers alone", wantSeeded: 0},
		{name: "with the seeded users", seed: true, wantSeeded: 1},
		{name: "from the Console's page", origin: console, wantOrigin: console},
		{name: "from another page", origin: "https://evil.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, store := newHandler(t, auth.Settings{Seed: tt.seed}, auth.Cookie{Name: auth.HostCookie})
			_ = store.SignUp(context.Background(), demo)
			req := httptest.NewRequest("GET", "/providers", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			checkProviders(t, rec, tt.wantSeeded)
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tt.wantOrigin {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tt.wantOrigin)
			}
		})
	}
}

func TestSignIn(t *testing.T) {
	tests := []struct {
		name         string
		target       string
		wantLocation string // a prefix
		wantFlow     bool
	}{
		{name: "Google", target: "/sign-in?provider=google&return=/orgs", wantLocation: "https://idp.example.com/auth?state=", wantFlow: true},
		{name: "an unknown provider", target: "/sign-in?provider=github", wantLocation: console + "/sign-in?error=unknown_provider"},
		{name: "no provider", target: "/sign-in", wantLocation: console + "/sign-in?error=unknown_provider"},
		{name: "a seeded user, seeding off", target: "/sign-in?user=owner", wantLocation: console + "/sign-in?error=unknown_provider"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, store := newHandler(t, auth.Settings{SignUp: auth.PolicyAnyone}, auth.Cookie{Name: auth.HostCookie})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", tt.target, nil))
			if rec.Code != 302 || !strings.HasPrefix(rec.Header().Get("Location"), tt.wantLocation) {
				t.Fatalf("got %d to %s, want 302 to %s…", rec.Code, rec.Header().Get("Location"), tt.wantLocation)
			}
			flow := cookieNamed(rec, auth.FlowCookie)
			if (flow != nil) != tt.wantFlow || (len(store.Flows) == 1) != tt.wantFlow {
				t.Fatalf("flow cookie %v and %d flows, want a flow %t", flow, len(store.Flows), tt.wantFlow)
			}
			if flow != nil && (!flow.HttpOnly || !flow.Secure || flow.SameSite != http.SameSiteLaxMode || flow.Path != "/" || flow.MaxAge != 600 || flow.Domain != "") {
				t.Errorf("flow cookie = %+v: wants HttpOnly, Secure, Lax, Path=/, 10 minutes, no Domain", flow)
			}
		})
	}
}

// checkProviders checks that rec lists Google and wantSeeded demo users.
func checkProviders(t *testing.T, rec *httptest.ResponseRecorder, wantSeeded int) {
	t.Helper()
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Providers []provider `json:"providers"`
		Seeded    []seeded   `json:"seeded"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Providers) != 1 || body.Providers[0] != (provider{ID: "google", Name: "Google"}) || len(body.Seeded) != wantSeeded {
		t.Errorf("body = %+v", body)
	}
	if wantSeeded == 1 && body.Seeded[0] != (seeded{Subject: "owner", DisplayName: "Demo Owner", Email: "owner@example.com"}) {
		t.Errorf("seeded = %+v", body.Seeded)
	}
}

// begin starts a sign-in and returns the state and the flow cookie.
func begin(t *testing.T, h http.Handler) (string, *http.Cookie) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/sign-in?provider=google&return=/orgs/acme", nil))
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("state"), cookieNamed(rec, auth.FlowCookie)
}

func TestCallback(t *testing.T) {
	bob := auth.SignUp{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: "Bob"},
		Identity: auth.Identity{Issuer: "https://other.example.com", Subject: "b", Email: "alice@example.com", EmailVerified: true}}
	bob.Identity.UserID = bob.Principal.ID
	tests := []struct {
		name      string
		settings  auth.Settings
		cookie    auth.Cookie
		existing  *auth.SignUp
		query     func(state string) string
		noCookie  bool
		wantTo    string
		wantToken bool
	}{
		{name: "signed in, one host", settings: auth.Settings{SignUp: auth.PolicyAnyone}, cookie: auth.Cookie{Name: auth.HostCookie}, query: func(s string) string { return "code=good&state=" + s }, wantTo: console + "/orgs/acme", wantToken: true},
		{name: "signed in, a parent domain", settings: auth.Settings{SignUp: auth.PolicyAnyone}, cookie: auth.Cookie{Name: auth.SecureCookie, Domain: "example.com"}, query: func(s string) string { return "code=good&state=" + s }, wantTo: console + "/orgs/acme", wantToken: true},
		{name: "the provider refused", settings: auth.Settings{SignUp: auth.PolicyAnyone}, cookie: auth.Cookie{Name: auth.HostCookie}, query: func(s string) string { return "error=access_denied&state=" + s }, wantTo: console + "/sign-in?error=provider"},
		{name: "a bad code", settings: auth.Settings{SignUp: auth.PolicyAnyone}, cookie: auth.Cookie{Name: auth.HostCookie}, query: func(s string) string { return "code=bad&state=" + s }, wantTo: console + "/sign-in?error=provider"},
		{name: "no flow cookie", settings: auth.Settings{SignUp: auth.PolicyAnyone}, cookie: auth.Cookie{Name: auth.HostCookie}, query: func(s string) string { return "code=good&state=" + s }, noCookie: true, wantTo: console + "/sign-in?error=flow"},
		{name: "an unknown state", settings: auth.Settings{SignUp: auth.PolicyAnyone}, cookie: auth.Cookie{Name: auth.HostCookie}, query: func(string) string { return "code=good&state=other" }, wantTo: console + "/sign-in?error=flow"},
		{name: "refused by the policy", settings: auth.Settings{SignUp: auth.PolicyInvited}, cookie: auth.Cookie{Name: auth.HostCookie}, query: func(s string) string { return "code=good&state=" + s }, wantTo: console + "/sign-in?error=refused"},
		{name: "an email another user holds", settings: auth.Settings{SignUp: auth.PolicyAnyone}, cookie: auth.Cookie{Name: auth.HostCookie}, existing: &bob, query: func(s string) string { return "code=good&state=" + s }, wantTo: console + "/sign-in?error=email_taken&issuer=https%3A%2F%2Fother.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, store := newHandler(t, tt.settings, tt.cookie)
			if tt.existing != nil {
				_ = store.SignUp(context.Background(), *tt.existing)
			}
			state, flow := begin(t, h)
			req := httptest.NewRequest("GET", "/callback?"+tt.query(state), nil)
			req.Header.Set("User-Agent", "test-browser")
			req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
			if !tt.noCookie {
				req.AddCookie(flow)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != 302 || rec.Header().Get("Location") != tt.wantTo {
				t.Fatalf("got %d to %s, want 302 to %s", rec.Code, rec.Header().Get("Location"), tt.wantTo)
			}
			if cleared := cookieNamed(rec, auth.FlowCookie); cleared == nil || cleared.MaxAge != -1 {
				t.Errorf("the flow cookie wasn't cleared: %v", cleared)
			}
			checkSessionCookie(t, rec, store, tt.cookie, tt.wantToken)
		})
	}
}

// checkSessionCookie checks that rec sets the session cookie if wantToken, and none otherwise, and that the store
// holds its session with the browser's details.
func checkSessionCookie(t *testing.T, rec *httptest.ResponseRecorder, store *authtest.Store, cookie auth.Cookie, wantToken bool) {
	t.Helper()
	session := cookieNamed(rec, cookie.Name)
	if (session != nil) != wantToken {
		t.Fatalf("session cookie = %v, want one %t", session, wantToken)
	}
	if session == nil {
		return
	}
	if !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteLaxMode || session.Path != "/" ||
		session.Domain != cookie.Domain || session.MaxAge != int((30*24*time.Hour).Seconds()) {
		t.Errorf("session cookie = %+v", session)
	}
	stored, _, err := store.Session(context.Background(), auth.Hash(session.Value))
	if err != nil || stored.UserAgent != "test-browser" || stored.IP != "203.0.113.7" {
		t.Errorf("session = %+v, %v: wants the browser's agent and forwarded address", stored, err)
	}
}

func TestSignInSeeded(t *testing.T) {
	h, store := newHandler(t, auth.Settings{Seed: true}, auth.Cookie{Name: auth.HostCookie})
	demo := auth.SignUp{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: "Demo"},
		Identity: auth.Identity{Issuer: auth.SeedIssuer, Subject: "owner", Email: "owner@example.com", EmailVerified: true}}
	demo.Identity.UserID = demo.Principal.ID
	_ = store.SignUp(context.Background(), demo)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/sign-in?user=owner&return=/orgs", nil))
	if rec.Code != 302 || rec.Header().Get("Location") != console+"/orgs" || cookieNamed(rec, auth.HostCookie) == nil {
		t.Errorf("got %d to %s with cookies %v, want 302 to the Console with a session", rec.Code, rec.Header().Get("Location"), rec.Result().Cookies())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/sign-in?user=nobody", nil))
	if rec.Header().Get("Location") != console+"/sign-in?error=unknown_provider" {
		t.Errorf("an unknown seeded user went to %s", rec.Header().Get("Location"))
	}
}

func TestSignOut(t *testing.T) {
	tests := []struct {
		name       string
		origin     string
		withCookie bool
		wantStatus int
		wantLive   int
	}{
		{name: "from the Console", origin: console, withCookie: true, wantStatus: 204, wantLive: 0},
		{name: "without an Origin", withCookie: true, wantStatus: 204, wantLive: 0},
		{name: "from another origin", origin: "https://evil.example.com", withCookie: true, wantStatus: 403, wantLive: 1},
		{name: "without a cookie", origin: console, wantStatus: 204, wantLive: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, store := newHandler(t, auth.Settings{SignUp: auth.PolicyAnyone}, auth.Cookie{Name: auth.HostCookie})
			session := signedIn(t, h)
			req := httptest.NewRequest("POST", "/sign-out", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.withCookie {
				req.AddCookie(session)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if cleared := cookieNamed(rec, auth.HostCookie); tt.wantStatus == 204 && (cleared == nil || cleared.MaxAge != -1) {
				t.Errorf("the session cookie wasn't cleared: %v", cleared)
			}
			if live := liveSessions(store); live != tt.wantLive {
				t.Errorf("%d live sessions, want %d", live, tt.wantLive)
			}
		})
	}
}

// signedIn signs in through h and returns the session cookie.
func signedIn(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	state, flow := begin(t, h)
	req := httptest.NewRequest("GET", "/callback?code=good&state="+state, nil)
	req.AddCookie(flow)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	session := cookieNamed(rec, auth.HostCookie)
	if session == nil {
		t.Fatal("no session")
	}
	return session
}

// liveSessions counts store's sessions that are not revoked.
func liveSessions(store *authtest.Store) int {
	live := 0
	for _, s := range store.ByToken {
		if s.RevokedAt.IsZero() {
			live++
		}
	}
	return live
}

func TestClient(t *testing.T) {
	tests := []struct {
		name   string
		remote string
		fwd    string
		want   string
	}{
		{name: "direct", remote: "192.0.2.1:4321", want: "192.0.2.1"},
		{name: "IPv6", remote: "[2001:db8::1]:4321", want: "2001:db8::1"},
		{name: "behind a proxy", remote: "10.0.0.1:4321", fwd: "203.0.113.7", want: "203.0.113.7"},
		{name: "behind two proxies", remote: "10.0.0.1:4321", fwd: "203.0.113.7, 10.0.0.2", want: "203.0.113.7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remote
			if tt.fwd != "" {
				r.Header.Set("X-Forwarded-For", tt.fwd)
			}
			if got := client(r).IP; got != tt.want {
				t.Errorf("client().IP = %q, want %q", got, tt.want)
			}
		})
	}
}
