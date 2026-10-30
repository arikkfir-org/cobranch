package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"cobranch.dev/internal/adapters/postgres/postgrestest"
	"cobranch.dev/internal/system/buildinfo"
)

// freeAddr returns a local address no one listens on yet.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().String()
}

// noRedirects answers a redirect as it is, so a test sees where the server sent the browser.
var noRedirects = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// get asks the server at addr for path and returns the status and body, or 0 while it doesn't answer.
func get(addr, method, path, body string) (int, string) {
	req, err := http.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
	if err != nil {
		return 0, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := noRedirects.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// request is a request to a server, and what it should answer.
type request struct {
	method, path, body string
	wantStatus         int
	wantBody           string // a part of the body
}

func TestRunServer(t *testing.T) {
	version := buildinfo.Version()
	health := []request{
		{method: "GET", path: "/healthz", wantStatus: 200, wantBody: "ok\n"},
		{method: "GET", path: "/readyz", wantStatus: 200, wantBody: "ready\n"},
	}
	tests := []struct {
		name     string
		api      api
		requests []request
	}{
		{
			name: "REST",
			api:  restServer,
			requests: append([]request{
				{method: "GET", path: "/v1/version", wantStatus: 200, wantBody: `{"version":"` + version + `"}` + "\n"},
				{method: "POST", path: "/", body: `{"query":"{ version }"}`, wantStatus: 404, wantBody: `"code":"route.not_found"`},
			}, health...),
		},
		{
			name: "GraphQL",
			api:  graphqlServer,
			requests: append([]request{
				{method: "POST", path: "/", body: `{"query":"{ version }"}`, wantStatus: 200, wantBody: `{"data":{"version":"` + version + `"}}`},
				{method: "POST", path: "/graphql", body: `{"query":"{ version }"}`, wantStatus: 404},
				{method: "GET", path: "/v1/version", wantStatus: 404},
			}, health...),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := startServer(t, tt.api)
			if status, body := waitReady(t, addr, func(status int) bool { return status != 0 }); status != 503 || body != "not ready: schema\n" {
				t.Fatalf("/readyz before cobranch migrate = %d %q, want 503 for the schema alone", status, body)
			}
			if err := runMigrate(context.Background(), io.Discard); err != nil {
				t.Fatalf("runMigrate() = %v", err)
			}
			if status, body := waitReady(t, addr, func(status int) bool { return status == 200 }); status != 200 {
				t.Fatalf("/readyz after cobranch migrate = %d %q, want 200", status, body)
			}
			for _, r := range tt.requests {
				status, body := get(addr, r.method, r.path, r.body)
				if status != r.wantStatus || !strings.Contains(body, r.wantBody) {
					t.Errorf("%s %s = %d %q, want %d with %q", r.method, r.path, status, body, r.wantStatus, r.wantBody)
				}
			}
		})
	}
}

// startServer runs a's server on a database of its own, never migrated, until the test ends, and returns its address.
func startServer(t *testing.T, a api) string {
	t.Helper()
	addr := freeAddr(t)
	t.Setenv("COBRANCH_HTTP_ADDR", addr)
	t.Setenv("COBRANCH_DATABASE_URL", postgrestest.Database(t))
	t.Setenv("COBRANCH_BLOB_URL", "file://"+t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServer(ctx, io.Discard, a) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("runServer() = %v after it was asked to stop", err)
		}
	})
	return addr
}

// waitReady asks the server at addr for /readyz until done accepts its status, for up to 30 seconds, and returns its
// last answer.
func waitReady(t *testing.T, addr string, done func(status int) bool) (int, string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		status, body := get(addr, "GET", "/readyz", "")
		if done(status) || time.Now().After(deadline) {
			return status, body
		}
		time.Sleep(100 * time.Millisecond)
	}
}
