package adminapi

import (
	"slices"
	"strings"
	"testing"

	"cobranch.dev/internal/services/auth"
)

// withOkta stores Okta, a provider an administrator added, at version 3.
func (f *fixture) withOkta() {
	f.store.Providers["okta"] = auth.StoredProvider{ID: "okta", Name: "Okta", Issuer: "https://acme.okta.com", ClientID: "o",
		SealedSecret: []byte("sealed:identity_providers/okta/client_secret:os"), Version: 3, CreatedAt: now, UpdatedAt: now}
}

func TestListProviders(t *testing.T) {
	f := newFixture(t)
	f.withOkta()
	rec := f.call(&f.ada, "GET", "/v1/providers", "")
	check(t, rec, 200, "")
	got := decode[struct{ Items []Provider }](t, rec.Body.Bytes())
	var listed []string
	for _, p := range got.Items {
		listed = append(listed, p.Id+" "+string(p.Source)+" "+deref(p.Issuer)+" "+deref(p.Preset))
	}
	if want := []string{"google values  google", "okta backOffice https://acme.okta.com "}; !slices.Equal(listed, want) {
		t.Errorf("GET /v1/providers = %q, want %q", listed, want)
	}
	if got.Items[0].UpdatedAt != nil || got.Items[1].UpdatedAt == nil || got.Items[1].UpdatedBy != nil {
		t.Errorf("GET /v1/providers = %+v: want times only on Okta, and nobody named", got.Items)
	}
	check(t, f.call(&f.olivia, "GET", "/v1/providers", ""), 403, "auth.forbidden")
	check(t, f.call(nil, "GET", "/v1/providers", ""), 401, "auth.unauthenticated")
}

func TestGetProvider(t *testing.T) {
	tests := []struct {
		name, path, noneMatch string
		wantStatus            int
		wantCode, wantETag    string
	}{
		{name: "the values' provider", path: "/v1/providers/google", wantStatus: 200, wantETag: `"values"`},
		{name: "an added provider", path: "/v1/providers/okta", wantStatus: 200, wantETag: `"3"`},
		{name: "unchanged since the caller's ETag", path: "/v1/providers/okta", noneMatch: `W/"3"`, wantStatus: 304, wantETag: `"3"`},
		{name: "no such provider", path: "/v1/providers/github", wantStatus: 404, wantCode: "provider.not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.withOkta()
			rec := f.callWith(&f.ada, "GET", tt.path, "", map[string]string{"If-None-Match": tt.noneMatch})
			check(t, rec, tt.wantStatus, tt.wantCode)
			if rec.Header().Get("ETag") != tt.wantETag {
				t.Errorf("ETag = %q, want %q", rec.Header().Get("ETag"), tt.wantETag)
			}
		})
	}
}

func TestAddProvider(t *testing.T) {
	tests := []struct {
		name, body   string
		wantStatus   int
		wantCode     string
		wantLocation string
	}{
		{name: "an issuer", body: `{"id":"auth0","name":"Auth0","issuer":"https://acme.auth0.com/","clientId":"a","clientSecret":"as"}`,
			wantStatus: 201, wantLocation: "/v1/providers/auth0"},
		{name: "a preset", body: `{"id":"gsuite","name":"Google Workspace","preset":"google","clientId":"g","clientSecret":"gs"}`,
			wantStatus: 201, wantLocation: "/v1/providers/gsuite"},
		{name: "the values' ID", body: `{"id":"google","name":"G","preset":"google","clientId":"g","clientSecret":"gs"}`, wantStatus: 409, wantCode: "provider.taken"},
		{name: "an added provider's ID", body: `{"id":"okta","name":"O","issuer":"https://acme.okta.com","clientId":"o","clientSecret":"os"}`, wantStatus: 409, wantCode: "provider.taken"},
		{name: "an issuer that doesn't answer", body: `{"id":"x","name":"X","issuer":"https://down.example.com","clientId":"x","clientSecret":"xs"}`, wantStatus: 400, wantCode: "issuer.unreachable"},
		{name: "no secret", body: `{"id":"x","name":"X","issuer":"https://x.example.com","clientId":"x","clientSecret":""}`, wantStatus: 400, wantCode: "client_secret.invalid"},
		{name: "not JSON", body: `{`, wantStatus: 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.withOkta()
			rec := f.call(&f.ada, "POST", "/v1/providers", tt.body)
			check(t, rec, tt.wantStatus, tt.wantCode)
			if rec.Header().Get("Location") != tt.wantLocation || (tt.wantStatus == 201 && rec.Header().Get("ETag") != `"1"`) {
				t.Errorf("Location %q and ETag %q, want %q and \"1\"", rec.Header().Get("Location"), rec.Header().Get("ETag"), tt.wantLocation)
			}
			if tt.wantStatus == 201 && (decode[Provider](t, rec.Body.Bytes()).Source != BackOffice || strings.Contains(rec.Body.String(), "clientSecret") ||
				strings.Contains(rec.Body.String(), `"as"`) || strings.Contains(rec.Body.String(), `"gs"`)) {
				t.Errorf("POST /v1/providers = %s: want an added provider, never its secret", rec.Body)
			}
		})
	}
}

func TestChangeProvider(t *testing.T) {
	tests := []struct {
		name, path, ifMatch, body string
		wantStatus                int
		wantCode, wantETag        string
	}{
		{name: "its name", path: "/v1/providers/okta", ifMatch: `"3"`, body: `{"name":"Acme's Okta"}`, wantStatus: 200, wantETag: `"4"`},
		{name: "a preset for its issuer", path: "/v1/providers/okta", ifMatch: `"3"`, body: `{"preset":"google"}`, wantStatus: 200, wantETag: `"4"`},
		{name: "a new secret", path: "/v1/providers/okta", ifMatch: "*", body: `{"clientSecret":"new"}`, wantStatus: 200, wantETag: `"4"`},
		{name: "without If-Match", path: "/v1/providers/okta", body: `{"name":"O"}`, wantStatus: 428, wantCode: "precondition.required"},
		{name: "changed meanwhile", path: "/v1/providers/okta", ifMatch: `"2"`, body: `{"name":"O"}`, wantStatus: 412, wantCode: "precondition.failed"},
		{name: "a weak ETag", path: "/v1/providers/okta", ifMatch: `W/"3"`, body: `{"name":"O"}`, wantStatus: 412, wantCode: "precondition.failed"},
		{name: "the values' provider", path: "/v1/providers/google", ifMatch: `"values"`, body: `{"name":"G"}`, wantStatus: 409, wantCode: "provider.locked"},
		{name: "no such provider", path: "/v1/providers/github", ifMatch: "*", body: `{"name":"G"}`, wantStatus: 404, wantCode: "provider.not_found"},
		{name: "an empty name", path: "/v1/providers/okta", ifMatch: `"3"`, body: `{"name":""}`, wantStatus: 400, wantCode: "name.invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.withOkta()
			header := map[string]string{"Content-Type": "application/merge-patch+json"}
			if tt.ifMatch != "" {
				header["If-Match"] = tt.ifMatch
			}
			rec := f.callWith(&f.ada, "PATCH", tt.path, tt.body, header)
			check(t, rec, tt.wantStatus, tt.wantCode)
			if rec.Header().Get("ETag") != tt.wantETag {
				t.Errorf("ETag = %q, want %q", rec.Header().Get("ETag"), tt.wantETag)
			}
		})
	}
}

func TestRemoveProvider(t *testing.T) {
	tests := []struct {
		name, path, ifMatch string
		viewer              func(*fixture) *auth.Authenticated
		wantStatus          int
		wantCode            string
	}{
		{name: "an added provider", path: "/v1/providers/okta", ifMatch: `"3"`, viewer: ada, wantStatus: 204},
		{name: "without If-Match", path: "/v1/providers/okta", viewer: ada, wantStatus: 428, wantCode: "precondition.required"},
		{name: "changed meanwhile", path: "/v1/providers/okta", ifMatch: `"2"`, viewer: ada, wantStatus: 412, wantCode: "precondition.failed"},
		{name: "the values' provider", path: "/v1/providers/google", ifMatch: "*", viewer: ada, wantStatus: 409, wantCode: "provider.locked"},
		{name: "not without installation.manage", path: "/v1/providers/okta", ifMatch: `"3"`, viewer: olivia, wantStatus: 403, wantCode: "auth.forbidden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.withOkta()
			header := map[string]string{}
			if tt.ifMatch != "" {
				header["If-Match"] = tt.ifMatch
			}
			check(t, f.callWith(tt.viewer(f), "DELETE", tt.path, "", header), tt.wantStatus, tt.wantCode)
			if _, kept := f.store.Providers["okta"]; kept == (tt.wantStatus == 204) {
				t.Errorf("Okta kept: %t after %d", kept, tt.wantStatus)
			}
		})
	}
}
