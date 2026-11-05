package graphql

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

func TestPreferences(t *testing.T) {
	self := func(alice auth.Authenticated) *auth.Authenticated { return &alice }
	agent := func(auth.Authenticated) *auth.Authenticated {
		return &auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindAgent}}}
	}
	dark := func(t *testing.T, service *auth.Service, alice auth.Authenticated) {
		t.Helper()
		theme := auth.ThemeDark
		if _, err := service.UpdatePreferences(context.Background(), alice, auth.PreferencesPatch{Theme: &theme}); err != nil {
			t.Fatal(err)
		}
	}
	read := `{ viewer { preferences { theme version } } }`
	tests := []struct {
		name     string
		seed     func(t *testing.T, service *auth.Service, alice auth.Authenticated)
		viewer   func(alice auth.Authenticated) *auth.Authenticated
		query    string
		wantBody []string
	}{
		{name: "read before a change", viewer: self, query: read, wantBody: []string{`"preferences":{"theme":"SYSTEM","version":0}`}},
		{name: "read after a change", seed: dark, viewer: self, query: read, wantBody: []string{`"preferences":{"theme":"DARK","version":1}`}},
		{name: "read as an agent", viewer: agent, query: read, wantBody: []string{`"code":"FORBIDDEN"`, `"reason":"preferences.people_only"`}},
		{name: "the first change", viewer: self, query: `mutation { updatePreferences(input: {theme: LIGHT}, expectedVersion: 0) { theme version } }`,
			wantBody: []string{`{"data":{"updatePreferences":{"theme":"LIGHT","version":1}}}`}},
		{name: "a change from whichever are there", seed: dark, viewer: self, query: `mutation { updatePreferences(input: {theme: SYSTEM}) { theme version } }`,
			wantBody: []string{`{"data":{"updatePreferences":{"theme":"SYSTEM","version":2}}}`}},
		{name: "a change from an older version", seed: dark, viewer: self, query: `mutation { updatePreferences(input: {theme: LIGHT}, expectedVersion: 0) { theme } }`,
			wantBody: []string{`"code":"CONFLICT"`, `"reason":"precondition.failed"`}},
		{name: "a version there never is", viewer: self, query: `mutation { updatePreferences(input: {theme: LIGHT}, expectedVersion: -1) { theme } }`,
			wantBody: []string{`"code":"BAD_USER_INPUT"`, `"reason":"version.invalid"`}},
		{name: "change as an agent", viewer: agent, query: `mutation { updatePreferences(input: {theme: DARK}) { theme } }`,
			wantBody: []string{`"code":"FORBIDDEN"`, `"reason":"preferences.people_only"`}},
		{name: "change as anonymous", query: `mutation { updatePreferences(input: {theme: DARK}) { theme } }`, wantBody: []string{`"code":"UNAUTHENTICATED"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, alice, _ := signedIn(t)
			if tt.seed != nil {
				tt.seed(t, service, alice)
			}
			var viewer *auth.Authenticated
			if tt.viewer != nil {
				viewer = tt.viewer(alice)
			}
			req := httptest.NewRequest("POST", "/", strings.NewReader(`{"query":`+jsonString(tt.query)+`}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			as(viewer, NewHandler("v1", "", service, emptyOrgs(), nil, nil, nil, nil, nil, nil)).ServeHTTP(rec, req)
			for _, want := range tt.wantBody {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("body = %s, want %s in it", rec.Body, want)
				}
			}
		})
	}
}
