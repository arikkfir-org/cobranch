package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/arikkfir-org/devhub/internal/adapters/postgres/postgrestest"
	"github.com/arikkfir-org/devhub/internal/system/buildinfo"
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

// get asks the server at addr for path and returns the status and body, or 0 while it doesn't answer.
func get(addr, method, path, body string) (int, string) {
	req, err := http.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
	if err != nil {
		return 0, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestRunAPIServer(t *testing.T) {
	version := buildinfo.Version()
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantBody   string
	}{
		{name: "liveness", method: "GET", path: "/healthz", wantStatus: 200, wantBody: "ok\n"},
		{name: "readiness", method: "GET", path: "/readyz", wantStatus: 200, wantBody: "ready\n"},
		{name: "REST", method: "GET", path: "/api/v1/version", wantStatus: 200, wantBody: `{"version":"` + version + `"}` + "\n"},
		{name: "GraphQL", method: "POST", path: "/graphql", body: `{"query":"{ version }"}`, wantStatus: 200, wantBody: `{"data":{"version":"` + version + `"}}`},
	}

	addr := freeAddr(t)
	t.Setenv("DEVHUB_HTTP_ADDR", addr)
	t.Setenv("DEVHUB_DATABASE_URL", postgrestest.Database(t))
	t.Setenv("DEVHUB_BLOB_URL", "file://"+t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runAPIServer(ctx, io.Discard) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("runAPIServer() = %v after it was asked to stop", err)
		}
	}()

	deadline := time.Now().Add(30 * time.Second)
	for status, body := get(addr, "GET", "/readyz", ""); status != http.StatusOK; status, body = get(addr, "GET", "/readyz", "") {
		if time.Now().After(deadline) {
			t.Fatalf("not ready after 30s: %d %s", status, body)
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := get(addr, tt.method, tt.path, tt.body)
			if status != tt.wantStatus || body != tt.wantBody {
				t.Errorf("%s %s = %d %q, want %d %q", tt.method, tt.path, status, body, tt.wantStatus, tt.wantBody)
			}
		})
	}
}
