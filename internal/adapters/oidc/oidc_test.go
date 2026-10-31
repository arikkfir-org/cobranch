package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc/oidctest"

	"cobranch.dev/internal/services/auth"
)

// idp is a fake identity provider: discovery and keys from oidctest, and a token endpoint that answers an ID token
// with the claims the test chose, or refuses.
type idp struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	// claims is the JSON the next ID token carries, besides iss, aud, exp and nonce; "" refuses the code.
	claims string
	// nonce is the nonce the next ID token carries, or the request's when "".
	nonce string
	// got records the token endpoint's last request.
	got url.Values
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &idp{key: key}
	ts := &oidctest.Server{PublicKeys: []oidctest.PublicKey{{PublicKey: &key.PublicKey, KeyID: "k1", Algorithm: "RS256"}}}
	mux := http.NewServeMux()
	mux.Handle("/", ts)
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		p.got = r.PostForm
		if p.claims == "" || r.PostForm.Get("code") != "good" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		nonce := p.nonce
		if nonce == "" {
			nonce = "n1"
		}
		claims := fmt.Sprintf(`{"iss":%q,"aud":"client","exp":%d,"iat":%d,"nonce":%q,%s}`,
			p.server.URL, time.Now().Add(time.Hour).Unix(), time.Now().Unix(), nonce, p.claims)
		idToken := oidctest.SignIDToken(key, "k1", "RS256", claims)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": idToken})
	})
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	ts.SetIssuer(p.server.URL)
	return p
}

func (p *idp) provider(t *testing.T) *Provider {
	t.Helper()
	prov, err := New(Config{ID: "test", Name: "Test", Issuer: p.server.URL, ClientID: "client", ClientSecret: "secret"},
		"https://auth.example.com/callback", p.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return prov
}

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		wantName string
		wantErr  bool
	}{
		{name: "complete", cfg: Config{ID: "google", Name: "Google", Issuer: auth.Presets["google"], ClientID: "c", ClientSecret: "s"}, wantName: "Google"},
		{name: "no name", cfg: Config{ID: "okta", Issuer: "https://okta.example.com", ClientID: "c", ClientSecret: "s"}, wantName: "okta"},
		{name: "no issuer", cfg: Config{ID: "okta", ClientID: "c", ClientSecret: "s"}, wantErr: true},
		{name: "no client ID", cfg: Config{ID: "okta", Issuer: "https://okta.example.com", ClientSecret: "s"}, wantErr: true},
		{name: "no client secret", cfg: Config{ID: "okta", Issuer: "https://okta.example.com", ClientID: "c"}, wantErr: true},
		{name: "no ID", cfg: Config{Issuer: "https://okta.example.com", ClientID: "c", ClientSecret: "s"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := New(tt.cfg, "https://auth.example.com/callback", nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New() error = %v, want error %t", err, tt.wantErr)
			}
			if err == nil && (p.ID() != tt.cfg.ID || p.Name() != tt.wantName) {
				t.Errorf("ID() = %q, Name() = %q, want %q and %q", p.ID(), p.Name(), tt.cfg.ID, tt.wantName)
			}
		})
	}
}

func TestAuthCodeURL(t *testing.T) {
	p := newIDP(t).provider(t)
	got, err := p.AuthCodeURL(context.Background(), "s1", "n1", "verifier-verifier-verifier-verifier-verifier")
	if err != nil {
		t.Fatalf("AuthCodeURL() = %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	want := map[string]string{
		"client_id": "client", "redirect_uri": "https://auth.example.com/callback", "response_type": "code",
		"scope": "openid email profile", "state": "s1", "nonce": "n1", "code_challenge_method": "S256",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if q.Get("code_challenge") == "" || !strings.HasSuffix(u.Path, "/auth") {
		t.Errorf("AuthCodeURL() = %s", got)
	}
}

func TestAuthCodeURLDiscoveryFails(t *testing.T) {
	p := newIDP(t)
	p.server.Close()
	if _, err := p.provider(t).AuthCodeURL(context.Background(), "s", "n", "v"); err == nil {
		t.Error("AuthCodeURL() = nil with the provider down")
	}
}

func TestDiscover(t *testing.T) {
	p := newIDP(t)
	down := newIDP(t)
	down.server.Close()
	tests := []struct {
		name    string
		issuer  string
		wantErr bool
	}{
		{name: "an issuer that answers", issuer: p.server.URL},
		{name: "a document that names another issuer", issuer: p.server.URL + "/other", wantErr: true},
		{name: "an issuer that is down", issuer: down.server.URL, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Discover(context.Background(), tt.issuer, p.server.Client()); (err != nil) != tt.wantErr {
				t.Errorf("Discover(%s) error = %v, want an error: %t", tt.issuer, err, tt.wantErr)
			}
		})
	}
}

func TestExchange(t *testing.T) {
	tests := []struct {
		name         string
		claims       string
		nonce        string
		code         string
		wantErr      bool
		wantEmail    string
		wantVerified bool
		wantName     string
	}{
		{name: "verified", claims: `"sub":"1","email":"alice@example.com","email_verified":true,"name":"Alice"`, code: "good", wantEmail: "alice@example.com", wantVerified: true, wantName: "Alice"},
		{name: "verified as a string", claims: `"sub":"1","email":"alice@example.com","email_verified":"true"`, code: "good", wantEmail: "alice@example.com", wantVerified: true},
		{name: "unverified", claims: `"sub":"1","email":"alice@example.com","email_verified":false`, code: "good", wantEmail: "alice@example.com"},
		{name: "no email", claims: `"sub":"1"`, code: "good"},
		{name: "a bad code", claims: `"sub":"1"`, code: "bad", wantErr: true},
		{name: "a refused code", code: "good", wantErr: true},
		{name: "another nonce", claims: `"sub":"1"`, nonce: "n2", code: "good", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idp := newIDP(t)
			idp.claims, idp.nonce = tt.claims, tt.nonce
			got, err := idp.provider(t).Exchange(context.Background(), tt.code, "verifier-verifier-verifier-verifier-verifier", "n1")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Exchange() error = %v, want error %t", err, tt.wantErr)
			}
			if idp.got.Get("code_verifier") != "verifier-verifier-verifier-verifier-verifier" || idp.got.Get("grant_type") != "authorization_code" {
				t.Errorf("the token request was %v: wants the PKCE verifier and the code grant", idp.got)
			}
			if err != nil {
				return
			}
			if got.Issuer != idp.server.URL || got.Subject != "1" || got.Email != tt.wantEmail || got.EmailVerified != tt.wantVerified || got.Name != tt.wantName {
				t.Errorf("Exchange() = %+v", got)
			}
		})
	}
}

func TestBoolish(t *testing.T) {
	tests := []struct {
		json    string
		want    bool
		wantErr bool
	}{
		{json: "true", want: true},
		{json: "false"},
		{json: `"true"`, want: true},
		{json: `"false"`},
		{json: "null"},
		{json: `"yes"`, wantErr: true},
		{json: "1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.json, func(t *testing.T) {
			var b boolish
			err := json.Unmarshal([]byte(tt.json), &b)
			if (err != nil) != tt.wantErr || bool(b) != tt.want {
				t.Errorf("boolish(%s) = %t, %v, want %t, error %t", tt.json, b, err, tt.want, tt.wantErr)
			}
		})
	}
}
