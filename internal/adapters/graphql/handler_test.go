package graphql

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewHandler(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		body       string
		wantStatus int
		wantBody   string
	}{
		{name: "version by POST", method: "POST", target: "/", body: `{"query":"{ version }"}`, wantStatus: 200, wantBody: `{"data":{"version":"v1.2.3"}}`},
		{name: "version by GET", method: "GET", target: "/?query=%7B%20version%20%7D", wantStatus: 200, wantBody: `{"data":{"version":"v1.2.3"}}`},
		{name: "introspection", method: "POST", target: "/", body: `{"query":"{ __type(name: \"Query\") { name } }"}`, wantStatus: 200, wantBody: `{"data":{"__type":{"name":"Query"}}}`},
		{name: "an unknown field", method: "POST", target: "/", body: `{"query":"{ secrets }"}`, wantStatus: 422},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.target, strings.NewReader(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			NewHandler("v1.2.3", nil, nil).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantBody != "" && strings.TrimSpace(rec.Body.String()) != tt.wantBody {
				t.Errorf("body = %s, want %s", rec.Body, tt.wantBody)
			}
		})
	}
}
