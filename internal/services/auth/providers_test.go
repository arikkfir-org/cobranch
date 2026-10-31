package auth_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/auth/authtest"
)

// stored is a provider the Back-office added, at version, its secret sealed for its ID by authtest.Sealer.
func stored(id string, version int64) auth.StoredProvider {
	return auth.StoredProvider{ID: id, Name: id, Issuer: "https://" + id + ".example.com", ClientID: "c-" + id,
		SealedSecret: []byte("sealed:" + auth.SecretAbout(id) + ":s-" + id), Version: version}
}

// builder records each configuration it builds, and refuses an ID in refuse.
type builder struct {
	built  []auth.ProviderConfig
	refuse string
}

func (b *builder) build(c auth.ProviderConfig) (auth.Provider, error) {
	if c.ID == b.refuse {
		return nil, errors.New("refused")
	}
	b.built = append(b.built, c)
	return &authtest.Provider{ProviderID: c.ID, Claims: alice}, nil
}

func TestProvidersStored(t *testing.T) {
	unsealable := stored("broken", 1)
	unsealable.SealedSecret = []byte("sealed:identity_providers/other/client_secret:s")
	preset := stored("gsuite", 1)
	preset.Issuer, preset.Preset = "", "google"
	tests := []struct {
		name      string
		stored    []auth.StoredProvider
		noStored  bool
		refuse    string
		want      []string
		wantBuilt []string
	}{
		{name: "the values' alone, without a secrets key", stored: []auth.StoredProvider{stored("okta", 1)}, noStored: true, want: []string{"google"}},
		{name: "the values', then the Back-office's", stored: []auth.StoredProvider{stored("auth0", 1), stored("okta", 1)},
			want: []string{"google", "auth0", "okta"}, wantBuilt: []string{"auth0 https://auth0.example.com s-auth0", "okta https://okta.example.com s-okta"}},
		{name: "a preset's issuer", stored: []auth.StoredProvider{preset}, want: []string{"google", "gsuite"}, wantBuilt: []string{"gsuite https://accounts.google.com s-gsuite"}},
		{name: "one the values shadow", stored: []auth.StoredProvider{stored("google", 1), stored("okta", 1)},
			want: []string{"google", "okta"}, wantBuilt: []string{"okta https://okta.example.com s-okta"}},
		{name: "one whose secret doesn't open", stored: []auth.StoredProvider{unsealable, stored("okta", 1)},
			want: []string{"google", "okta"}, wantBuilt: []string{"okta https://okta.example.com s-okta"}},
		{name: "one the adapter refuses", stored: []auth.StoredProvider{stored("okta", 1)}, refuse: "okta", want: []string{"google"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, store, _ := newService(auth.Settings{}, alice)
			store.Stored = tt.stored
			b := &builder{refuse: tt.refuse}
			if !tt.noStored {
				s.UseStoredProviders(authtest.Sealer{}, b.build)
			}
			got, err := s.Providers(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var ids, built []string
			for _, p := range got {
				ids = append(ids, p.ID())
			}
			for _, c := range b.built {
				built = append(built, c.ID+" "+c.Issuer+" "+c.ClientSecret)
			}
			if !slices.Equal(ids, tt.want) || !slices.Equal(built, tt.wantBuilt) {
				t.Errorf("Providers() = %q, built %q; want %q, built %q", ids, built, tt.want, tt.wantBuilt)
			}
		})
	}
}

// TestProvidersStoredVersions lists the providers three times: the second reuses what the first built, and the third
// builds again the one whose version changed, and forgets the one removed.
func TestProvidersStoredVersions(t *testing.T) {
	s, store, _ := newService(auth.Settings{}, alice)
	b := &builder{}
	s.UseStoredProviders(authtest.Sealer{}, b.build)
	rounds := []struct {
		stored    []auth.StoredProvider
		wantBuilt int
	}{
		{stored: []auth.StoredProvider{stored("auth0", 1), stored("okta", 1)}, wantBuilt: 2},
		{stored: []auth.StoredProvider{stored("auth0", 1), stored("okta", 1)}, wantBuilt: 2},
		{stored: []auth.StoredProvider{stored("okta", 2)}, wantBuilt: 3},
		{stored: []auth.StoredProvider{stored("auth0", 1), stored("okta", 2)}, wantBuilt: 4},
	}
	for i, r := range rounds {
		store.Stored = r.stored
		if _, err := s.Providers(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(b.built) != r.wantBuilt {
			t.Errorf("round %d built %d providers in all, want %d", i+1, len(b.built), r.wantBuilt)
		}
	}
}

func TestBeginStored(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		fail     error
		wantErr  error
	}{
		{name: "the values' provider", provider: "google"},
		{name: "a Back-office provider", provider: "okta"},
		{name: "an unknown provider", provider: "github", wantErr: auth.ErrUnknownProvider},
		{name: "a store that fails", provider: "okta", fail: errDown, wantErr: errDown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, store, _ := newService(auth.Settings{}, alice)
			store.Stored = []auth.StoredProvider{stored("okta", 1)}
			s.UseStoredProviders(authtest.Sealer{}, (&builder{}).build)
			store.Fail = tt.fail
			_, err := s.Begin(context.Background(), tt.provider, "/")
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Begin(%s) error = %v, want %v", tt.provider, err, tt.wantErr)
			}
		})
	}
}

func TestCompleteStored(t *testing.T) {
	s, store, _ := newService(auth.Settings{SignUp: auth.PolicyAnyone}, alice)
	store.Stored = []auth.StoredProvider{stored("okta", 1)}
	s.UseStoredProviders(authtest.Sealer{}, (&builder{}).build)
	begun, err := s.Begin(context.Background(), "okta", "/orgs")
	if err != nil {
		t.Fatal(err)
	}
	state := stateOf(t, begun.RedirectURL)
	store.Stored = nil // removed while the person was at the provider
	if _, err := s.Complete(context.Background(), "good", state, begun.BrowserToken, auth.Client{}); !errors.Is(err, auth.ErrUnknownProvider) {
		t.Errorf("Complete() at a removed provider error = %v, want ErrUnknownProvider", err)
	}
	store.Stored = []auth.StoredProvider{stored("okta", 1)}
	begun, _ = s.Begin(context.Background(), "okta", "/orgs")
	got, err := s.Complete(context.Background(), "good", stateOf(t, begun.RedirectURL), begun.BrowserToken, auth.Client{})
	if err != nil || got.ReturnPath != "/orgs" {
		t.Errorf("Complete() at a Back-office provider = %+v, %v", got, err)
	}
}
