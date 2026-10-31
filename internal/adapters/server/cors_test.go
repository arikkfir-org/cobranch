package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowOrigin(t *testing.T) {
	const console = "https://console.example.com"
	tests := []struct {
		name        string
		allowed     string
		method      string
		origin      string
		preflight   string // the method a preflight request asks for
		wantStatus  int
		wantAllowed string
		wantMethods string
		wantHeaders string
		wantCreds   string
		wantHandled bool
	}{
		{name: "the Console's call", allowed: console, method: "GET", origin: console, wantStatus: 200, wantAllowed: console, wantCreds: "true", wantHandled: true},
		{name: "the Console's preflight", allowed: console, method: "OPTIONS", origin: console, preflight: "POST", wantStatus: 204, wantAllowed: console, wantMethods: "GET, POST, PATCH, DELETE", wantHeaders: "Content-Type, X-Request-Id, traceparent, tracestate", wantCreds: "true"},
		{name: "the Console's preflight for a PATCH", allowed: console, method: "OPTIONS", origin: console, preflight: "PATCH", wantStatus: 204, wantAllowed: console, wantMethods: "GET, POST, PATCH, DELETE", wantHeaders: "Content-Type, X-Request-Id, traceparent, tracestate", wantCreds: "true"},
		{name: "the Console's preflight for a DELETE", allowed: console, method: "OPTIONS", origin: console, preflight: "DELETE", wantStatus: 204, wantAllowed: console, wantMethods: "GET, POST, PATCH, DELETE", wantHeaders: "Content-Type, X-Request-Id, traceparent, tracestate", wantCreds: "true"},
		{name: "another origin's call", allowed: console, method: "GET", origin: "https://evil.example.com", wantStatus: 200, wantHandled: true},
		{name: "another origin's preflight", allowed: console, method: "OPTIONS", origin: "https://evil.example.com", preflight: "POST", wantStatus: 200, wantHandled: true},
		{name: "a call from no page", allowed: console, method: "GET", wantStatus: 200, wantHandled: true},
		{name: "no origin allowed", method: "GET", origin: console, wantStatus: 200, wantHandled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handled := false
			h := AllowOrigin(tt.allowed, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				handled = true
				_, _ = io.WriteString(w, "ok")
			}))
			req := httptest.NewRequest(tt.method, "/v1/version", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.preflight != "" {
				req.Header.Set("Access-Control-Request-Method", tt.preflight)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus || handled != tt.wantHandled {
				t.Errorf("status = %d, handled %t, want %d and %t", rec.Code, handled, tt.wantStatus, tt.wantHandled)
			}
			for header, want := range map[string]string{
				"Access-Control-Allow-Origin":      tt.wantAllowed,
				"Access-Control-Allow-Methods":     tt.wantMethods,
				"Access-Control-Allow-Headers":     tt.wantHeaders,
				"Access-Control-Allow-Credentials": tt.wantCreds,
			} {
				if got := rec.Header().Get(header); got != want {
					t.Errorf("%s = %q, want %q", header, got, want)
				}
			}
		})
	}
}
