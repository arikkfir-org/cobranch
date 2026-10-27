package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewHandler(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		path        string
		wantStatus  int
		wantType    string
		wantVersion string
		wantCode    string
	}{
		{name: "version", method: "GET", path: "/api/v1/version", wantStatus: 200, wantType: "application/json", wantVersion: "v1.2.3"},
		{name: "an unknown operation", method: "GET", path: "/api/v1/nothing", wantStatus: 404, wantType: "application/problem+json", wantCode: "route.not_found"},
		{name: "an unknown version of the API", method: "GET", path: "/api/v9/version", wantStatus: 404, wantType: "application/problem+json", wantCode: "route.not_found"},
		{name: "a method the operation lacks", method: "DELETE", path: "/api/v1/version", wantStatus: 404, wantType: "application/problem+json", wantCode: "route.not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			NewHandler("v1.2.3").ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if got := rec.Header().Get("Content-Type"); got != tt.wantType {
				t.Errorf("Content-Type = %q, want %q", got, tt.wantType)
			}
			checkBody(t, rec, tt.wantVersion, tt.wantCode)
		})
	}
}

// checkBody checks that rec's body names wantVersion, or is a problem with wantCode and rec's status.
func checkBody(t *testing.T, rec *httptest.ResponseRecorder, wantVersion, wantCode string) {
	t.Helper()
	var body struct {
		Version string `json:"version"`
		Code    string `json:"code"`
		Status  int    `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body, err)
	}
	if body.Version != wantVersion || body.Code != wantCode {
		t.Errorf("body = %+v, want version %q and code %q", body, wantVersion, wantCode)
	}
	if wantCode != "" && body.Status != rec.Code {
		t.Errorf("problem's status = %d, want %d", body.Status, rec.Code)
	}
}

func TestErrorHandlers(t *testing.T) {
	tests := []struct {
		name       string
		handle     func(http.ResponseWriter, *http.Request, error)
		wantStatus int
		wantCode   string
		wantDetail string
	}{
		{name: "a request the API can't read", handle: badRequest, wantStatus: 400, wantCode: "request.invalid", wantDetail: "bad input"},
		{name: "a failed operation", handle: internalError, wantStatus: 500, wantCode: "internal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/api/v1/version", nil).WithContext(context.Background())
			tt.handle(rec, req, errorString("bad input"))
			var p Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
			detail := ""
			if p.Detail != nil {
				detail = *p.Detail
			}
			if rec.Code != tt.wantStatus || p.Status != tt.wantStatus || p.Code != tt.wantCode || detail != tt.wantDetail {
				t.Errorf("got %d %+v (detail %q), want %d, code %q, detail %q", rec.Code, p, detail, tt.wantStatus, tt.wantCode, tt.wantDetail)
			}
		})
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }
