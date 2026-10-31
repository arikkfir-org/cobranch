package admin_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/auth/authtest"
	"cobranch.dev/internal/services/orgs"
)

// withProviders sets f's service up with the values' Google, a sealer unless noKey, and a discovery that fails for
// down.example.com, recording each issuer it checks in discovered.
func withProviders(f *fixture, noKey bool, discovered *[]string) {
	setup := admin.ProviderSetup{
		Values: []auth.ProviderConfig{{ID: "google", Name: "Google", Issuer: "https://accounts.google.com", Preset: "google", ClientID: "g", ClientSecret: "gs"}},
		Sealer: authtest.Sealer{},
		Discover: func(_ context.Context, issuer string) error {
			*discovered = append(*discovered, issuer)
			if strings.Contains(issuer, "down.example.com") {
				return errors.New("connection refused")
			}
			return nil
		},
	}
	if noKey {
		setup.Sealer = nil
	}
	f.service.SetProviders(setup)
}

// okta is a provider an administrator added.
func okta() auth.StoredProvider {
	return auth.StoredProvider{ID: "okta", Name: "Okta", Issuer: "https://acme.okta.com", ClientID: "o",
		SealedSecret: []byte("sealed:identity_providers/okta/client_secret:os"), Version: 3, CreatedAt: now, UpdatedAt: now}
}

func TestProviders(t *testing.T) {
	f := newFixture(t)
	var discovered []string
	withProviders(f, false, &discovered)
	shadowed := okta()
	shadowed.ID = "google"
	f.store.Providers["okta"], f.store.Providers["google"] = okta(), shadowed
	got, err := f.service.Providers(context.Background(), f.ada)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"google values  google false", "google backOffice https://acme.okta.com  true", "okta backOffice https://acme.okta.com  false"}
	var listed []string
	for _, p := range got {
		listed = append(listed, strings.Join([]string{p.ID, p.Source, p.Issuer, p.Preset, map[bool]string{true: "true", false: "false"}[p.Shadowed]}, " "))
	}
	if !slices.Equal(listed, want) {
		t.Errorf("Providers() = %q, want %q", listed, want)
	}
	if _, err := f.service.Providers(context.Background(), f.uma); !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("Providers() as Uma error = %v, want ErrForbidden", err)
	}
}

func TestAddProvider(t *testing.T) {
	good := admin.NewProvider{ID: "okta", Name: "Okta", Issuer: " https://acme.okta.com ", ClientID: "o", ClientSecret: "os"}
	with := func(change func(p *admin.NewProvider)) admin.NewProvider {
		p := good
		change(&p)
		return p
	}
	tests := []struct {
		name           string
		p              admin.NewProvider
		taken, noKey   bool
		viewer         func(f *fixture) auth.User
		wantErr        error
		wantCode       string
		wantDiscovered []string
	}{
		{name: "an issuer", p: good, wantDiscovered: []string{"https://acme.okta.com"}},
		{name: "a preset", p: with(func(p *admin.NewProvider) { p.ID, p.Issuer, p.Preset = "google-2", "", "google" }), wantDiscovered: []string{"https://accounts.google.com"}},
		{name: "the values' provider's ID", p: with(func(p *admin.NewProvider) { p.ID = "google" }), wantErr: admin.ErrProviderTaken},
		{name: "another added provider's ID", p: good, taken: true, wantErr: admin.ErrProviderTaken, wantDiscovered: []string{"https://acme.okta.com"}},
		{name: "an ID that isn't one", p: with(func(p *admin.NewProvider) { p.ID = "My IdP" }), wantCode: "provider_id.invalid"},
		{name: "no name", p: with(func(p *admin.NewProvider) { p.Name = " " }), wantCode: "name.invalid"},
		{name: "neither issuer nor preset", p: with(func(p *admin.NewProvider) { p.Issuer = "" }), wantCode: "issuer.required"},
		{name: "both issuer and preset", p: with(func(p *admin.NewProvider) { p.Preset = "google" }), wantCode: "issuer.required"},
		{name: "an unknown preset", p: with(func(p *admin.NewProvider) { p.Issuer, p.Preset = "", "github" }), wantCode: "preset.invalid"},
		{name: "an http issuer", p: with(func(p *admin.NewProvider) { p.Issuer = "http://acme.okta.com" }), wantCode: "issuer.invalid"},
		{name: "an issuer with a query", p: with(func(p *admin.NewProvider) { p.Issuer = "https://acme.okta.com?x=1" }), wantCode: "issuer.invalid"},
		{name: "no client ID", p: with(func(p *admin.NewProvider) { p.ClientID = "" }), wantCode: "client_id.invalid"},
		{name: "no client secret", p: with(func(p *admin.NewProvider) { p.ClientSecret = "" }), wantCode: "client_secret.invalid"},
		{name: "an issuer that doesn't answer", p: with(func(p *admin.NewProvider) { p.Issuer = "https://down.example.com" }), wantCode: "issuer.unreachable", wantDiscovered: []string{"https://down.example.com"}},
		{name: "no secrets key", p: good, noKey: true, wantErr: admin.ErrNoSecretsKey, wantDiscovered: []string{"https://acme.okta.com"}},
		{name: "not an administrator", p: good, viewer: func(f *fixture) auth.User { return f.uma }, wantErr: auth.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			var discovered []string
			withProviders(f, tt.noKey, &discovered)
			if tt.taken {
				f.store.Providers["okta"] = okta()
			}
			v := f.ada
			if tt.viewer != nil {
				v = tt.viewer(f)
			}
			got, err := f.service.AddProvider(context.Background(), v, tt.p)
			if !slices.Equal(discovered, tt.wantDiscovered) {
				t.Errorf("discovered %q, want %q", discovered, tt.wantDiscovered)
			}
			if failed(t, "AddProvider", err, tt.wantErr, tt.wantCode) {
				return
			}
			checkAdded(t, f, got, tt.p.ID)
		})
	}
}

// checkAdded checks that provider id was written by Ada at version 1, its secret sealed for it.
func checkAdded(t *testing.T, f *fixture, got admin.Provider, id string) {
	t.Helper()
	stored := f.store.Providers[id]
	if got.ID != id || got.Source != admin.SourceBackOffice || got.Version != 1 || got.UpdatedBy != f.ada.ID ||
		string(stored.SealedSecret) != "sealed:identity_providers/"+id+"/client_secret:os" || got.Fingerprint() != "1" {
		t.Errorf("AddProvider() = %+v, stored %+v", got, stored)
	}
}

func TestChangeProvider(t *testing.T) {
	str := func(s string) *string { return &s }
	tests := []struct {
		name           string
		id, match      string
		change         admin.ProviderChange
		viewer         func(f *fixture) auth.User
		want           func(p auth.StoredProvider) auth.StoredProvider
		wantErr        error
		wantCode       string
		wantDiscovered []string
	}{
		{name: "its name", id: "okta", match: "3", change: admin.ProviderChange{Name: str("Acme's Okta")},
			want: func(p auth.StoredProvider) auth.StoredProvider { p.Name = "Acme's Okta"; return p }},
		{name: "its secret, sealed again", id: "okta", match: "*", change: admin.ProviderChange{ClientSecret: str("new")},
			want: func(p auth.StoredProvider) auth.StoredProvider {
				p.SealedSecret = []byte("sealed:identity_providers/okta/client_secret:new")
				return p
			}},
		{name: "a preset for its issuer", id: "okta", match: "3", change: admin.ProviderChange{Preset: str("google")}, wantDiscovered: []string{"https://accounts.google.com"},
			want: func(p auth.StoredProvider) auth.StoredProvider { p.Issuer, p.Preset = "", "google"; return p }},
		{name: "another issuer", id: "okta", match: "3", change: admin.ProviderChange{Issuer: str("https://login.acme.com")}, wantDiscovered: []string{"https://login.acme.com"},
			want: func(p auth.StoredProvider) auth.StoredProvider { p.Issuer = "https://login.acme.com"; return p }},
		{name: "an issuer that doesn't answer", id: "okta", match: "3", change: admin.ProviderChange{Issuer: str("https://down.example.com")}, wantCode: "issuer.unreachable", wantDiscovered: []string{"https://down.example.com"}},
		{name: "an empty secret", id: "okta", match: "3", change: admin.ProviderChange{ClientSecret: str("")}, wantCode: "client_secret.invalid"},
		{name: "an empty name", id: "okta", match: "3", change: admin.ProviderChange{Name: str("")}, wantCode: "name.invalid"},
		{name: "changed meanwhile", id: "okta", match: "2", change: admin.ProviderChange{Name: str("Okta")}, wantErr: admin.ErrStale},
		{name: "the values' provider", id: "google", match: "*", change: admin.ProviderChange{Name: str("G")}, wantErr: admin.ErrLocked},
		{name: "no such provider", id: "github", match: "*", change: admin.ProviderChange{Name: str("G")}, wantErr: orgs.ErrNotFound},
		{name: "not an administrator", id: "okta", match: "3", change: admin.ProviderChange{Name: str("Okta")}, viewer: func(f *fixture) auth.User { return f.max }, wantErr: auth.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			var discovered []string
			withProviders(f, false, &discovered)
			f.store.Providers["okta"] = okta()
			v := f.ada
			if tt.viewer != nil {
				v = tt.viewer(f)
			}
			got, err := f.service.ChangeProvider(context.Background(), v, tt.id, tt.match, tt.change)
			if !slices.Equal(discovered, tt.wantDiscovered) {
				t.Errorf("discovered %q, want %q", discovered, tt.wantDiscovered)
			}
			if failed(t, "ChangeProvider", err, tt.wantErr, tt.wantCode) {
				if f.store.Providers["okta"].Version != 3 {
					t.Errorf("a refused change was written: %+v", f.store.Providers["okta"])
				}
				return
			}
			checkChangedProvider(t, f, got, tt.want(okta()))
		})
	}
}

// checkChangedProvider checks that the stored provider is want at version 4, changed by Ada, and got shows it.
func checkChangedProvider(t *testing.T, f *fixture, got admin.Provider, want auth.StoredProvider) {
	t.Helper()
	stored := f.store.Providers[want.ID]
	if stored.Name != want.Name || stored.Issuer != want.Issuer || stored.Preset != want.Preset || stored.ClientID != want.ClientID ||
		string(stored.SealedSecret) != string(want.SealedSecret) || stored.Version != 4 || stored.UpdatedBy != f.ada.ID {
		t.Errorf("stored %+v, want %+v at version 4 by Ada", stored, want)
	}
	if got.Name != want.Name || got.Issuer != want.Issuer || got.Preset != want.Preset || got.Fingerprint() != "4" {
		t.Errorf("ChangeProvider() = %+v", got)
	}
}

func TestRemoveProvider(t *testing.T) {
	tests := []struct {
		name, id, match string
		viewer          func(f *fixture) auth.User
		wantErr         error
	}{
		{name: "an added provider", id: "okta", match: "3"},
		{name: "any version", id: "okta", match: "*"},
		{name: "changed meanwhile", id: "okta", match: "2", wantErr: admin.ErrStale},
		{name: "the values' provider", id: "google", match: "*", wantErr: admin.ErrLocked},
		{name: "no such provider", id: "github", match: "*", wantErr: orgs.ErrNotFound},
		{name: "not an administrator", id: "okta", match: "3", viewer: func(f *fixture) auth.User { return f.uma }, wantErr: auth.ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			var discovered []string
			withProviders(f, false, &discovered)
			f.store.Providers["okta"] = okta()
			v := f.ada
			if tt.viewer != nil {
				v = tt.viewer(f)
			}
			err := f.service.RemoveProvider(context.Background(), v, tt.id, tt.match)
			if failed(t, "RemoveProvider", err, tt.wantErr, "") {
				if _, ok := f.store.Providers["okta"]; !ok {
					t.Error("a refused removal removed it")
				}
				return
			}
			if _, ok := f.store.Providers["okta"]; ok {
				t.Error("RemoveProvider() left it")
			}
		})
	}
}

func TestProvider(t *testing.T) {
	f := newFixture(t)
	var discovered []string
	withProviders(f, false, &discovered)
	f.store.Providers["okta"] = okta()
	tests := []struct {
		id, wantSource, wantFingerprint string
		wantErr                         error
	}{
		{id: "google", wantSource: admin.SourceValues, wantFingerprint: "values"},
		{id: "okta", wantSource: admin.SourceBackOffice, wantFingerprint: "3"},
		{id: "github", wantErr: orgs.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got, err := f.service.Provider(context.Background(), f.ada, tt.id)
			if failed(t, "Provider", err, tt.wantErr, "") {
				return
			}
			if got.Source != tt.wantSource || got.Fingerprint() != tt.wantFingerprint {
				t.Errorf("Provider(%s) = %+v", tt.id, got)
			}
		})
	}
}
