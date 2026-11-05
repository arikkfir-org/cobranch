package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

func TestMyPreferences(t *testing.T) {
	dark := func(t *testing.T, service *auth.Service, alice auth.Authenticated) {
		t.Helper()
		theme := auth.ThemeDark
		if _, err := service.UpdatePreferences(context.Background(), alice, auth.PreferencesPatch{Theme: &theme}); err != nil {
			t.Fatal(err)
		}
	}
	agent := func(auth.Authenticated) *auth.Authenticated {
		return &auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindAgent}}}
	}
	patch := func(tag string) http.Header {
		h := http.Header{"Content-Type": {"application/merge-patch+json"}}
		if tag != "" {
			h.Set("If-Match", tag)
		}
		return h
	}
	tests := []preferencesCase{
		{name: "read as anonymous", method: "GET", wantStatus: 401, wantCode: "auth.unauthenticated"},
		{name: "read before a change", viewer: self, method: "GET", wantStatus: 200, wantTheme: System, wantTag: `"0"`},
		{name: "read after a change", seed: dark, viewer: self, method: "GET", wantStatus: 200, wantTheme: Dark, wantTag: `"1"`},
		{name: "read the ones held", seed: dark, viewer: self, method: "GET", header: http.Header{"If-None-Match": {`"1"`}}, wantStatus: 304, wantTag: `"1"`},
		{name: "read as an agent", viewer: agent, method: "GET", wantStatus: 403, wantCode: "preferences.people_only"},
		{name: "change as anonymous", method: "PATCH", header: patch(`"0"`), body: `{"theme":"dark"}`, wantStatus: 401, wantCode: "auth.unauthenticated"},
		{name: "the first change", viewer: self, method: "PATCH", header: patch(`"0"`), body: `{"theme":"dark"}`, wantStatus: 200, wantTheme: Dark, wantTag: `"1"`},
		{name: "a change from the version read", seed: dark, viewer: self, method: "PATCH", header: patch(`"1"`), body: `{"theme":"light"}`,
			wantStatus: 200, wantTheme: Light, wantTag: `"2"`},
		{name: "a change from whichever are there", seed: dark, viewer: self, method: "PATCH", header: patch("*"), body: `{"theme":"system"}`,
			wantStatus: 200, wantTheme: System, wantTag: `"2"`},
		{name: "a change that names nothing keeps them", seed: dark, viewer: self, method: "PATCH", header: patch(`"1"`), body: `{}`,
			wantStatus: 200, wantTheme: Dark, wantTag: `"1"`},
		{name: "a change from an older version", seed: dark, viewer: self, method: "PATCH", header: patch(`"0"`), body: `{"theme":"light"}`,
			wantStatus: 412, wantCode: "precondition.failed"},
		{name: "a change without If-Match", viewer: self, method: "PATCH", header: patch(""), body: `{"theme":"light"}`,
			wantStatus: 428, wantCode: "precondition.required"},
		{name: "a change from a weak tag", viewer: self, method: "PATCH", header: patch(`W/"0"`), body: `{"theme":"light"}`,
			wantStatus: 412, wantCode: "precondition.failed"},
		{name: "a theme there isn't", viewer: self, method: "PATCH", header: patch(`"0"`), body: `{"theme":"sepia"}`,
			wantStatus: 400, wantCode: "preferences.theme_invalid"},
		{name: "change as an agent", viewer: agent, method: "PATCH", header: patch("*"), body: `{"theme":"dark"}`,
			wantStatus: 403, wantCode: "preferences.people_only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { runPreferencesCase(t, tt) })
	}
}

// preferencesCase is a request to /v1/me/preferences, after seed, and what it answers.
type preferencesCase struct {
	name   string
	seed   func(t *testing.T, service *auth.Service, alice auth.Authenticated)
	viewer func(alice auth.Authenticated) *auth.Authenticated
	method string
	header http.Header
	body   string
	// wantStatus, and wantCode for a refusal; wantTheme and wantTag for an answer.
	wantStatus int
	wantCode   string
	wantTheme  Theme
	wantTag    string
}

// runPreferencesCase serves tt's request as Alice, signed in with fresh preferences, and checks its answer.
func runPreferencesCase(t *testing.T, tt preferencesCase) {
	t.Helper()
	service, alice, _ := signedIn(t)
	if tt.seed != nil {
		tt.seed(t, service, alice)
	}
	var viewer *auth.Authenticated
	if tt.viewer != nil {
		viewer = tt.viewer(alice)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(tt.method, "/v1/me/preferences", strings.NewReader(tt.body))
	for k, v := range tt.header {
		req.Header[k] = v
	}
	as(viewer, NewHandler("v1", service, emptyOrgs(t), nil, nil, nil, nil, nil, nil, nil)).ServeHTTP(rec, req)
	if rec.Code != tt.wantStatus {
		t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
	}
	if tt.wantCode != "" {
		checkBody(t, rec, "", tt.wantCode)
		return
	}
	if got := rec.Header().Get("ETag"); got != tt.wantTag {
		t.Errorf("ETag = %s, want %s", got, tt.wantTag)
	}
	if tt.wantStatus == http.StatusNotModified {
		return
	}
	if got := decode[Preferences](t, rec.Body.Bytes()); got.Theme != tt.wantTheme || `"`+strconv.FormatInt(got.Version, 10)+`"` != tt.wantTag {
		t.Errorf("preferences = %+v, want %s at %s", got, tt.wantTheme, tt.wantTag)
	}
}
