package main

import (
	"context"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"cobranch.dev/internal/adapters/postgres/postgrestest"
	"cobranch.dev/internal/system/buildinfo"
)

// ports holds the next port freeAddr tries, from a random start, so that two runs on one machine rarely meet.
var ports struct {
	sync.Mutex
	next int
}

// freeAddr returns a local address no one listens on yet, for a workload to bind. Its port lies below the kernel's
// ephemeral range, from which the kernel takes the ports of outgoing connections and of listeners on port 0: the tests,
// this package's and those running beside it, connect all the time, and one of their connections could take a port of
// that range between freeAddr's check and the workload's bind. Below the range only a bind that names a port takes it,
// and freeAddr never hands out a port twice in a run.
func freeAddr(t *testing.T) string {
	t.Helper()
	ports.Lock()
	defer ports.Unlock()
	const first, room = 10000, 1000 // The lowest port it tries, and the ports a run has after its random start.
	low := ephemeralLow()
	if low < first+2*room {
		// No room below the range: a port the kernel picks is the best there is.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		_ = ln.Close() // Only the address matters: the workload binds it anew.
		return ln.Addr().String()
	}
	if ports.next == 0 {
		ports.next = first + rand.IntN(low-first-room)
	}
	var err error
	for ; ports.next < low; ports.next++ {
		var ln net.Listener
		if ln, err = net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.next))); err == nil {
			_ = ln.Close() // Only the address matters: the workload binds it anew.
			ports.next++
			return ln.Addr().String()
		}
	}
	t.Fatalf("no free port below %d, the ephemeral range's first: %v", low, err)
	return ""
}

// ephemeralLow returns the first port of the kernel's ephemeral range: Linux's, or else 32768, its default, which lies
// below the ranges of macOS and Windows.
func ephemeralLow() int {
	if b, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range"); err == nil {
		if f := strings.Fields(string(b)); len(f) == 2 {
			if low, err := strconv.Atoi(f[0]); err == nil {
				return low
			}
		}
	}
	return 32768
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
		{
			name: "Admin",
			api:  adminServer,
			requests: append([]request{
				{method: "GET", path: "/v1/version", wantStatus: 200, wantBody: `{"version":"` + version + `"}` + "\n"},
				{method: "GET", path: "/v1/orgs", wantStatus: 401, wantBody: `"code":"auth.unauthenticated"`},
				{method: "GET", path: "/v1/providers", wantStatus: 401, wantBody: `"code":"auth.unauthenticated"`},
				{method: "POST", path: "/", body: `{"query":"{ version }"}`, wantStatus: 404, wantBody: `"code":"route.not_found"`},
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
