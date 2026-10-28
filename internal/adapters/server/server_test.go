package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// echo answers with its name and the request's ID.
func echo(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, name+" "+RequestID(r.Context()))
	})
}

func TestHandler(t *testing.T) {
	pass := func(context.Context) error { return nil }
	fail := func(context.Context) error { return errors.New("down") }
	tests := []struct {
		name       string
		pattern    string
		method     string
		path       string
		requestID  string
		checks     map[string]Check
		wantStatus int
		wantBody   string
		wantID     string
	}{
		{name: "liveness", pattern: "/{$}", method: "GET", path: "/healthz", wantStatus: 200, wantBody: "ok\n"},
		{name: "liveness while a check fails", pattern: "/{$}", method: "GET", path: "/healthz", checks: map[string]Check{"database": fail}, wantStatus: 200, wantBody: "ok\n"},
		{name: "ready", pattern: "/{$}", method: "GET", path: "/readyz", checks: map[string]Check{"database": pass, "bucket": pass}, wantStatus: 200, wantBody: "ready\n"},
		{name: "not ready", pattern: "/{$}", method: "GET", path: "/readyz", checks: map[string]Check{"schema": fail, "database": pass, "bucket": fail}, wantStatus: 503, wantBody: "not ready: bucket, schema\n"},
		{name: "an API at every other path", pattern: "/", method: "GET", path: "/v1/version", requestID: "abc-1", wantStatus: 200, wantBody: "api abc-1", wantID: "abc-1"},
		{name: "health beside an API at every other path", pattern: "/", method: "GET", path: "/healthz", wantStatus: 200, wantBody: "ok\n"},
		{name: "an API at the root alone", pattern: "/{$}", method: "POST", path: "/", requestID: "abc.2", wantStatus: 200, wantBody: "api abc.2", wantID: "abc.2"},
		{name: "a path beside an API at the root alone", pattern: "/{$}", method: "GET", path: "/metrics", wantStatus: 404},
		{name: "liveness by POST", pattern: "/{$}", method: "POST", path: "/healthz", wantStatus: 405},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := Handler(tt.pattern, echo("api"), tt.checks)
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.requestID != "" {
				req.Header.Set(RequestIDHeader, tt.requestID)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantBody != "" && rec.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
			id := rec.Header().Get(RequestIDHeader)
			if id == "" || (tt.wantID != "" && id != tt.wantID) {
				t.Errorf("%s = %q, want %q", RequestIDHeader, id, tt.wantID)
			}
		})
	}
}

func TestRequestID(t *testing.T) {
	tests := []struct {
		name     string
		callerID string
		wantKept bool
	}{
		{name: "none", callerID: ""},
		{name: "a sane ID", callerID: "4bf92f3577b34da6", wantKept: true},
		{name: "too long", callerID: strings.Repeat("a", 129)},
		{name: "a header injection", callerID: "abc\r\nSet-Cookie: x=1"},
		{name: "spaces", callerID: "a b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen string
			h := withRequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = RequestID(r.Context()) }))
			req := httptest.NewRequest("GET", "/", nil)
			req.Header[RequestIDHeader] = []string{tt.callerID}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			got := rec.Header().Get(RequestIDHeader)
			if got != seen || got == "" {
				t.Fatalf("response names %q, the handler saw %q", got, seen)
			}
			if kept := got == tt.callerID; kept != tt.wantKept {
				t.Errorf("request ID %q kept = %t, want %t", got, kept, tt.wantKept)
			}
		})
	}
	if got := RequestID(context.Background()); got != "" {
		t.Errorf("RequestID() outside a request = %q, want none", got)
	}
}

func TestServe(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		timeout  time.Duration
		wantErr  bool
	}{
		{name: "a request finishes within the timeout", duration: 200 * time.Millisecond, timeout: 5 * time.Second},
		{name: "a request outlasts the timeout", duration: 2 * time.Second, timeout: 100 * time.Millisecond, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ln, err := Listen(ctx, "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				close(started)
				time.Sleep(tt.duration)
				_, _ = io.WriteString(w, "done")
			})
			served := make(chan error, 1)
			go func() { served <- Serve(ctx, ln, slow, tt.timeout) }()

			got := make(chan string, 1)
			go func() {
				resp, err := http.Get("http://" + ln.Addr().String()) //nolint:noctx // the test's request outlives nothing
				if err != nil {
					got <- err.Error()
					return
				}
				defer func() { _ = resp.Body.Close() }()
				body, _ := io.ReadAll(resp.Body)
				got <- string(body)
			}()
			<-started
			cancel()
			if err := <-served; (err != nil) != tt.wantErr {
				t.Fatalf("Serve() error = %v, want error %t", err, tt.wantErr)
			}
			if body := <-got; !tt.wantErr && body != "done" {
				t.Errorf("the request in flight got %q, want it to finish", body)
			}
		})
	}
}
