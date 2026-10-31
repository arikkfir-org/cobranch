package main

import (
	"context"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"cobranch.dev/internal/adapters/postgres/postgrestest"
	"cobranch.dev/internal/system/config"
)

func TestRunAuthServer(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		seedBefore bool
		requests   []request
	}{
		{
			name: "no provider, seeding on",
			env:  map[string]string{"COBRANCH_SEED": "true"},
			requests: []request{
				{method: "GET", path: "/healthz", wantStatus: 200, wantBody: "ok\n"},
				{method: "GET", path: "/providers", wantStatus: 200, wantBody: `"providers":[],"seeded":[{"subject":"admin"`},
				{method: "GET", path: "/sign-in?user=owner&return=/orgs", wantStatus: 302},
				{method: "GET", path: "/sign-in?provider=google", wantStatus: 302},
				{method: "POST", path: "/sign-out", wantStatus: 204},
				{method: "GET", path: "/v1/version", wantStatus: 404},
			},
		},
		{
			name: "a Google client, seeding off",
			env: map[string]string{
				"COBRANCH_AUTH_PROVIDERS":            `[{"id":"google","name":"Google","preset":"google","clientId":"c"}]`,
				"COBRANCH_AUTH_CLIENT_SECRET_GOOGLE": "s",
			},
			requests: []request{
				{method: "GET", path: "/providers", wantStatus: 200, wantBody: `{"providers":[{"id":"google","name":"Google"}],"seeded":[]}`},
				{method: "GET", path: "/sign-in?user=owner", wantStatus: 302},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := freeAddr(t)
			t.Setenv("COBRANCH_HTTP_ADDR", addr)
			t.Setenv("COBRANCH_DATABASE_URL", postgrestest.Database(t))
			t.Setenv("COBRANCH_CONSOLE_URL", "https://cobranch.test")
			t.Setenv("COBRANCH_AUTH_URL", "https://cobranch.test/auth")
			unset(t, "COBRANCH_SEED", "COBRANCH_AUTH_PROVIDERS", "COBRANCH_AUTH_CLIENT_SECRET_GOOGLE")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			startAuthServer(t, addr)
			for _, r := range tt.requests {
				status, body := get(addr, r.method, r.path, r.body)
				if status != r.wantStatus || !strings.Contains(body, r.wantBody) {
					t.Errorf("%s %s = %d %q, want %d with %q", r.method, r.path, status, body, r.wantStatus, r.wantBody)
				}
			}
		})
	}
}

func TestRunAuthServerConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{name: "no URLs", env: map[string]string{}, wantErr: "COBRANCH_CONSOLE_URL and COBRANCH_AUTH_URL"},
		{name: "a bad policy", env: map[string]string{"COBRANCH_CONSOLE_URL": "https://cobranch.test", "COBRANCH_AUTH_URL": "https://cobranch.test/auth", "COBRANCH_AUTH_SIGN_UP": "everyone"}, wantErr: "not a sign-up policy"},
		{name: "an unknown preset", env: map[string]string{"COBRANCH_CONSOLE_URL": "https://cobranch.test", "COBRANCH_AUTH_URL": "https://cobranch.test/auth", "COBRANCH_AUTH_PROVIDERS": `[{"id":"x","preset":"nope","clientId":"c"}]`, "COBRANCH_AUTH_CLIENT_SECRET_X": "s"}, wantErr: "no preset"},
		{name: "hosts without a parent", env: map[string]string{"COBRANCH_CONSOLE_URL": "https://cobranch.test", "COBRANCH_AUTH_URL": "https://auth.example"}, wantErr: "naming the session cookie"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unset(t, "COBRANCH_CONSOLE_URL", "COBRANCH_AUTH_URL", "COBRANCH_AUTH_SIGN_UP", "COBRANCH_AUTH_PROVIDERS", "COBRANCH_SEED")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := runAuthServer(ctx, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("runAuthServer() = %v, want %q in it", err, tt.wantErr)
			}
		})
	}
}

// startAuthServer migrates the database the environment names, then runs the Auth server on addr until the test
// ends, and waits for it to be ready.
func startAuthServer(t *testing.T, addr string) {
	t.Helper()
	if err := runMigrate(context.Background(), io.Discard); err != nil {
		t.Fatalf("runMigrate() = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runAuthServer(ctx, io.Discard) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("runAuthServer() = %v after it was asked to stop", err)
		}
	})
	if status, body := waitReady(t, addr, func(status int) bool { return status == 200 }); status != 200 {
		t.Fatalf("/readyz = %d %q, want 200", status, body)
	}
}

// unset unsets names for the test, and sets them back when the test ends.
func unset(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		t.Setenv(name, "") // records the value to restore
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunMaintenance(t *testing.T) {
	t.Setenv("COBRANCH_DATABASE_URL", postgrestest.Database(t))
	if err := runMigrate(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := runMaintenance(context.Background(), io.Discard); err != nil {
		t.Errorf("runMaintenance() = %v", err)
	}
}

func TestAuthSettings(t *testing.T) {
	backOffice := config.URL{URL: &url.URL{Scheme: "https", Host: "admin.cobranch.test", Path: "/back-office"}}
	tests := []struct {
		name        string
		cfg         config.AuthServer
		wantReturns []string
	}{
		{name: "without a Back-office", cfg: config.AuthServer{SignUp: "anyone"}},
		{name: "with a Back-office", cfg: config.AuthServer{SignUp: "anyone", URLs: config.URLs{BackOffice: backOffice}},
			wantReturns: []string{"https://admin.cobranch.test/back-office"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings, err := authSettings(tt.cfg)
			if err != nil {
				t.Fatal(err)
			}
			var returns []string
			for _, u := range settings.ReturnURLs {
				returns = append(returns, u.String())
			}
			if !slices.Equal(returns, tt.wantReturns) {
				t.Errorf("ReturnURLs = %v, want %v", returns, tt.wantReturns)
			}
		})
	}
}
