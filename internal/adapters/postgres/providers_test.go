package postgres

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"

	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

func TestIdentityProviders(t *testing.T) {
	f := newOrgFixture(t)
	ctx := context.Background()
	okta := auth.StoredProvider{ID: "okta", Name: "Okta", Issuer: "https://acme.okta.com", ClientID: "o", SealedSecret: []byte{1, 2, 3}}
	gsuite := auth.StoredProvider{ID: "gsuite", Name: "Google Workspace", Preset: "google", ClientID: "g", SealedSecret: []byte{4}}
	tests := []struct {
		name    string
		write   func() (auth.StoredProvider, error)
		wantErr error
		want    []string
	}{
		{name: "an issuer", write: func() (auth.StoredProvider, error) { return f.s.CreateIdentityProvider(ctx, f.olivia, okta, now) },
			want: []string{"okta https://acme.okta.com  1"}},
		{name: "a preset", write: func() (auth.StoredProvider, error) { return f.s.CreateIdentityProvider(ctx, f.olivia, gsuite, now) },
			want: []string{"gsuite  google 1", "okta https://acme.okta.com  1"}},
		{name: "a taken ID", write: func() (auth.StoredProvider, error) { return f.s.CreateIdentityProvider(ctx, f.olivia, okta, now) },
			wantErr: admin.ErrProviderTaken, want: []string{"gsuite  google 1", "okta https://acme.okta.com  1"}},
		{name: "a change", write: func() (auth.StoredProvider, error) {
			changed := okta
			changed.Issuer, changed.Preset, changed.Version = "", "google", 1
			return f.s.ChangeIdentityProvider(ctx, f.olivia, changed, now)
		}, want: []string{"gsuite  google 1", "okta  google 2"}},
		{name: "a change from an old version", write: func() (auth.StoredProvider, error) {
			stale := okta
			stale.Version = 1
			return f.s.ChangeIdentityProvider(ctx, f.olivia, stale, now)
		}, wantErr: admin.ErrStale, want: []string{"gsuite  google 1", "okta  google 2"}},
		{name: "a removal from an old version", write: func() (auth.StoredProvider, error) {
			return auth.StoredProvider{}, f.s.DeleteIdentityProvider(ctx, f.olivia, "okta", 1)
		}, wantErr: admin.ErrStale, want: []string{"gsuite  google 1", "okta  google 2"}},
		{name: "a removal", write: func() (auth.StoredProvider, error) {
			return auth.StoredProvider{}, f.s.DeleteIdentityProvider(ctx, f.olivia, "okta", 2)
		}, want: []string{"gsuite  google 1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.write(); !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if got := listProviders(t, NewAuthStore(f.pool)); !slices.Equal(got, tt.want) {
				t.Errorf("IdentityProviders() = %q, want %q", got, tt.want)
			}
		})
	}
	if _, err := f.s.IdentityProvider(ctx, "okta"); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("IdentityProvider(a removed one) error = %v, want orgs.ErrNotFound", err)
	}
	checkProvidersAudited(t, f)
}

// listProviders lists the stored providers as "id issuer preset version", checking each one's secret and author.
func listProviders(t *testing.T, s *AuthStore) []string {
	t.Helper()
	stored, err := s.IdentityProviders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range stored {
		if len(p.SealedSecret) == 0 || p.UpdatedAt.IsZero() {
			t.Errorf("provider %s = %+v: no secret, or no time", p.ID, p)
		}
		out = append(out, p.ID+" "+p.Issuer+" "+p.Preset+" "+strconv.FormatInt(p.Version, 10))
	}
	return out
}

// checkProvidersAudited checks that every change is in the audit log, by Olivia, without the client secret.
func checkProvidersAudited(t *testing.T, f *orgFixture) {
	t.Helper()
	log := auditLog(t, f.pool, "identity_providers")
	var actions []string
	for _, entry := range log {
		actions = append(actions, entry.Action)
		if entry.Principal == nil || *entry.Principal != f.olivia {
			t.Errorf("audit entry %+v isn't Olivia's", entry)
		}
		for _, row := range []map[string]any{entry.Before, entry.After} {
			if _, leaked := row["client_secret"]; leaked {
				t.Errorf("audit entry %+v holds the client secret", entry)
			}
		}
	}
	if !slices.Equal(actions, []string{"insert", "insert", "update", "delete"}) {
		t.Errorf("audit log actions = %q, want two inserts, an update and a delete", actions)
	}
}
