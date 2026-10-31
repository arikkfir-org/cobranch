package adminapi

import (
	"net/http/httptest"
	"slices"
	"testing"

	"cobranch.dev/internal/services/auth"
)

// read GETs the settings as Ada and returns their ETag.
func (f *fixture) read(t *testing.T) string {
	t.Helper()
	rec := f.call(&f.ada, "GET", "/v1/settings", "")
	if rec.Code != 200 || rec.Header().Get("ETag") == "" {
		t.Fatalf("GET /v1/settings = %d with ETag %q: %s", rec.Code, rec.Header().Get("ETag"), rec.Body)
	}
	return rec.Header().Get("ETag")
}

func TestGetSettings(t *testing.T) {
	tests := []struct {
		name       string
		values     auth.Settings
		viewer     func(*fixture) *auth.Authenticated
		noneMatch  func(etag string) string
		wantStatus int
		wantCode   string
		wantSignUp SignUpPolicy
		wantLocked []SettingsLocked
	}{
		{name: "the defaults", viewer: ada, wantStatus: 200, wantSignUp: "anyone", wantLocked: []SettingsLocked{}},
		{name: "locked by the values", values: auth.Settings{SignUp: auth.PolicyInvited}, viewer: ada, wantStatus: 200, wantSignUp: "invited",
			wantLocked: []SettingsLocked{SignUp}},
		{name: "unchanged since the caller's ETag", viewer: ada, noneMatch: func(e string) string { return e }, wantStatus: 304},
		{name: "unchanged, the ETag weak and among others", viewer: ada, noneMatch: func(e string) string { return `"x", W/` + e }, wantStatus: 304},
		{name: "changed since another ETag", viewer: ada, noneMatch: func(string) string { return `"x"` }, wantStatus: 200, wantSignUp: "anyone",
			wantLocked: []SettingsLocked{}},
		{name: "not without installation.manage", viewer: olivia, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "Anonymous", wantStatus: 401, wantCode: "auth.unauthenticated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixtureWith(t, tt.values)
			header := map[string]string{}
			if tt.noneMatch != nil {
				header["If-None-Match"] = tt.noneMatch(f.read(t))
			}
			var viewer *auth.Authenticated
			if tt.viewer != nil {
				viewer = tt.viewer(f)
			}
			rec := f.callWith(viewer, "GET", "/v1/settings", "", header)
			check(t, rec, tt.wantStatus, tt.wantCode)
			if rec.Header().Get("Cache-Control") != "private, no-cache" {
				t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
			}
			if tt.wantStatus != 200 {
				return
			}
			got := decode[Settings](t, rec.Body.Bytes())
			if got.SignUp != tt.wantSignUp || !got.SelfServe || !slices.Equal(got.Locked, tt.wantLocked) || got.SignUpDomains == nil ||
				rec.Header().Get("ETag") == "" {
				t.Errorf("settings = %+v with ETag %q", got, rec.Header().Get("ETag"))
			}
		})
	}
}

func TestChangeSettings(t *testing.T) {
	off := false
	tests := []struct {
		name       string
		values     auth.Settings
		viewer     func(*fixture) *auth.Authenticated
		ifMatch    func(etag string) string
		body       string
		wantStatus int
		wantCode   string
		check      func(t *testing.T, f *fixture, got Settings)
	}{
		{name: "the policy and its domains", viewer: ada, ifMatch: same, body: `{"signUp":"domains","signUpDomains":["Example.com"]}`, wantStatus: 200,
			check: func(t *testing.T, f *fixture, got Settings) {
				if got.SignUp != "domains" || !slices.Equal(got.SignUpDomains, []string{"example.com"}) || !got.SelfServe ||
					got.UpdatedBy == nil || *got.UpdatedBy != f.ada.User.ID || f.store.Installation.Version != 2 {
					t.Errorf("settings = %+v, stored %+v", got, f.store.Installation)
				}
			}},
		{name: "self-serve off", viewer: ada, ifMatch: same, body: `{"selfServe":false}`, wantStatus: 200,
			check: func(t *testing.T, _ *fixture, got Settings) {
				if got.SelfServe || got.SignUp != "anyone" {
					t.Errorf("settings = %+v", got)
				}
			}},
		{name: "no If-Match", viewer: ada, body: `{"selfServe":false}`, wantStatus: 428, wantCode: "precondition.required"},
		{name: "an old ETag", viewer: ada, ifMatch: func(string) string { return `"0123"` }, body: `{"selfServe":false}`, wantStatus: 412, wantCode: "precondition.failed"},
		{name: "a weak ETag", viewer: ada, ifMatch: func(e string) string { return "W/" + e }, body: `{"selfServe":false}`, wantStatus: 412, wantCode: "precondition.failed"},
		{name: "a setting the values lock", values: auth.Settings{SelfServe: &off}, viewer: ada, ifMatch: same, body: `{"selfServe":true}`, wantStatus: 409, wantCode: "setting.locked"},
		{name: "not a policy", viewer: ada, ifMatch: same, body: `{"signUp":"everyone"}`, wantStatus: 400, wantCode: "sign_up.invalid"},
		{name: "the domains policy without a domain", viewer: ada, ifMatch: same, body: `{"signUp":"domains"}`, wantStatus: 400, wantCode: "sign_up_domains.required"},
		{name: "a body that isn't JSON", viewer: ada, ifMatch: same, body: `selfServe=false`, wantStatus: 400, wantCode: "request.invalid"},
		{name: "not without installation.manage", viewer: olivia, ifMatch: same, body: `{"selfServe":false}`, wantStatus: 403, wantCode: "auth.forbidden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixtureWith(t, tt.values)
			header := map[string]string{"Content-Type": "application/merge-patch+json"}
			if tt.ifMatch != nil {
				header["If-Match"] = tt.ifMatch(f.read(t))
			}
			rec := f.callWith(tt.viewer(f), "PATCH", "/v1/settings", tt.body, header)
			check(t, rec, tt.wantStatus, tt.wantCode)
			if tt.check == nil {
				if f.store.Installation.Version != 1 {
					t.Errorf("a refused change was written: %+v", f.store.Installation)
				}
				return
			}
			tt.check(t, f, decode[Settings](t, rec.Body.Bytes()))
			if now := f.read(t); rec.Header().Get("ETag") != now {
				t.Errorf("the change's ETag %s isn't the settings' %s", rec.Header().Get("ETag"), now)
			}
		})
	}
}

// same is the ETag the caller read.
func same(etag string) string { return etag }

// check checks rec's status and, for a problem, its code.
func check(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d: %s", rec.Code, wantStatus, rec.Body)
	}
	if wantCode != "" {
		if p := decode[Problem](t, rec.Body.Bytes()); p.Code != wantCode {
			t.Errorf("problem = %+v, want code %s", p, wantCode)
		}
	}
}
