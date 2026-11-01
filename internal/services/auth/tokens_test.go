package auth_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/auth/authtest"
)

func TestValidateToken(t *testing.T) {
	tests := []struct {
		name            string
		in              auth.TokenInput
		wantCode        string
		wantPermissions []string
		wantExpiresAt   time.Time
	}{
		{name: "the defaults", in: auth.TokenInput{Name: " Laptop "},
			wantPermissions: []string{"repo.push", "repo.read"}, wantExpiresAt: now.Add(auth.TokenLifetime)},
		{name: "permissions listed twice, out of order", in: auth.TokenInput{Name: "CI", Permissions: []string{"repo.read", "org.read", "repo.read"}, ExpiresAt: now.Add(time.Hour)},
			wantPermissions: []string{"org.read", "repo.read"}, wantExpiresAt: now.Add(time.Hour)},
		{name: "a year to the minute", in: auth.TokenInput{Name: "CI", ExpiresAt: now.Add(auth.TokenMaxLifetime)},
			wantPermissions: []string{"repo.push", "repo.read"}, wantExpiresAt: now.Add(auth.TokenMaxLifetime)},
		{name: "a year by a client's clock a minute ahead", in: auth.TokenInput{Name: "CI", ExpiresAt: now.Add(auth.TokenMaxLifetime + time.Minute)},
			wantPermissions: []string{"repo.push", "repo.read"}, wantExpiresAt: now.Add(auth.TokenMaxLifetime + time.Minute)},
		{name: "no name", in: auth.TokenInput{Name: "  "}, wantCode: "token.name_invalid"},
		{name: "a long name", in: auth.TokenInput{Name: strings.Repeat("n", 101)}, wantCode: "token.name_invalid"},
		{name: "a permission that isn't one", in: auth.TokenInput{Name: "CI", Permissions: []string{"repo.read", "repo.everything"}}, wantCode: "token.permission_unknown"},
		{name: "an end that passed", in: auth.TokenInput{Name: "CI", ExpiresAt: now}, wantCode: "token.expiry_invalid"},
		{name: "an end past a year", in: auth.TokenInput{Name: "CI", ExpiresAt: now.AddDate(1, 0, 1)}, wantCode: "token.expiry_invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := auth.ValidateToken(tt.in, now)
			var verr *auth.ValidationError
			if tt.wantCode != "" {
				if !errors.As(err, &verr) || verr.Code != tt.wantCode {
					t.Fatalf("ValidateToken() error = %v, want code %s", err, tt.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateToken() error = %v", err)
			}
			if got.Name != strings.TrimSpace(tt.in.Name) || !slices.Equal(got.Permissions, tt.wantPermissions) || !got.ExpiresAt.Equal(tt.wantExpiresAt) {
				t.Errorf("ValidateToken() = %+v, want permissions %v ending %v", got, tt.wantPermissions, tt.wantExpiresAt)
			}
		})
	}
}

// signedInAs returns the service, its store and who a fresh session of Alice's resolves to.
func signedInAs(t *testing.T) (*auth.Service, *authtest.Store, auth.Authenticated) {
	t.Helper()
	s, store, _ := newService(auth.Settings{SignUp: auth.PolicyAnyone}, alice)
	a, err := s.Authenticate(context.Background(), signedIn(t, s))
	if err != nil {
		t.Fatal(err)
	}
	return s, store, a
}

func TestCreateToken(t *testing.T) {
	key := auth.User{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindAPIKey, DisplayName: "Deploy"}}
	tests := []struct {
		name    string
		as      func(a auth.Authenticated) auth.Authenticated
		in      auth.TokenInput
		fail    bool
		wantErr error
	}{
		{name: "from a session", as: func(a auth.Authenticated) auth.Authenticated { return a }, in: auth.TokenInput{Name: "Laptop"}},
		{name: "from a personal token", as: func(a auth.Authenticated) auth.Authenticated {
			return auth.Authenticated{User: a.User, Credential: auth.Credential{ID: uuid.New(), Kind: auth.CredentialPersonalToken}}
		}, in: auth.TokenInput{Name: "Laptop"}, wantErr: auth.ErrForbidden},
		{name: "from an API key", as: func(auth.Authenticated) auth.Authenticated {
			return auth.Authenticated{User: key, Credential: auth.Credential{ID: uuid.New(), Kind: auth.CredentialAPIKey}}
		}, in: auth.TokenInput{Name: "Laptop"}, wantErr: auth.ErrForbidden},
		{name: "from nobody", as: func(auth.Authenticated) auth.Authenticated { return auth.Authenticated{} }, in: auth.TokenInput{Name: "Laptop"}, wantErr: auth.ErrForbidden},
		{name: "without a name", as: func(a auth.Authenticated) auth.Authenticated { return a }, wantErr: &auth.ValidationError{}},
		{name: "a store that fails", as: func(a auth.Authenticated) auth.Authenticated { return a }, in: auth.TokenInput{Name: "Laptop"}, fail: true, wantErr: errDown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, store, a := signedInAs(t)
			if tt.fail {
				store.Fail = errDown
			}
			token, secret, err := s.CreateToken(context.Background(), tt.as(a), tt.in)
			var verr *auth.ValidationError
			if _, wantInvalid := tt.wantErr.(*auth.ValidationError); wantInvalid {
				if !errors.As(err, &verr) {
					t.Fatalf("CreateToken() error = %v, want a ValidationError", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CreateToken() error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			checkToken(t, store, a, token, secret)
		})
	}
}

// checkToken checks that token is a's, from a's session, recorded, and that its secret is shaped as a personal token's.
func checkToken(t *testing.T, store *authtest.Store, a auth.Authenticated, token auth.Credential, secret string) {
	t.Helper()
	if !auth.ValidSecret(auth.PrefixPersonalToken, secret) || token.Hint != auth.Hint(secret) {
		t.Errorf("secret %q, hint %q: not a personal token's", secret, token.Hint)
	}
	if token.Kind != auth.CredentialPersonalToken || token.PrincipalID != a.User.ID || token.CreatedBy != a.User.ID {
		t.Errorf("token = %+v, want a personal token of %s", token, a.User.ID)
	}
	if token.Issuer != alice.Issuer || token.Subject != alice.Subject {
		t.Errorf("token's identity = %s|%s, want the session's", token.Issuer, token.Subject)
	}
	if !token.ExpiresAt.Equal(now.Add(auth.TokenLifetime)) || !slices.Equal(token.Permissions, auth.DefaultTokenPermissions) {
		t.Errorf("token ends %v with %v, want the defaults", token.ExpiresAt, token.Permissions)
	}
	if stored, ok := store.Credentials[string(auth.Hash(secret))]; !ok || stored.ID != token.ID {
		t.Errorf("the store keeps %+v under the secret's hash, want the token", stored)
	}
}

func TestAuthenticateToken(t *testing.T) {
	tests := []struct {
		name      string
		change    func(c *auth.Credential, store *authtest.Store, a auth.Authenticated)
		at        time.Time
		wantErr   error
		wantTouch bool
	}{
		{name: "a live token", at: now.Add(time.Hour), wantTouch: true},
		{name: "a token used lately", change: func(c *auth.Credential, _ *authtest.Store, _ auth.Authenticated) {
			c.LastUsedAt = now.Add(time.Hour - time.Second)
		},
			at: now.Add(time.Hour)},
		{name: "an expired token", at: now.Add(auth.TokenLifetime), wantErr: auth.ErrUnauthenticated},
		{name: "a revoked token", change: func(c *auth.Credential, _ *authtest.Store, _ auth.Authenticated) { c.RevokedAt = now.Add(time.Minute) },
			at: now.Add(time.Hour), wantErr: auth.ErrUnauthenticated},
		{name: "a suspended user", change: func(_ *auth.Credential, store *authtest.Store, a auth.Authenticated) {
			u := store.Users[a.User.ID]
			u.Suspended = true
			store.Users[a.User.ID] = u
		}, at: now.Add(time.Hour), wantErr: auth.ErrUnauthenticated},
		{name: "an API key's secret under the token's prefix", change: func(c *auth.Credential, _ *authtest.Store, _ auth.Authenticated) { c.Kind = auth.CredentialAPIKey },
			at: now.Add(time.Hour), wantErr: auth.ErrUnauthenticated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, store, a := signedInAs(t)
			ctx := context.Background()
			token, secret, err := s.CreateToken(ctx, a, auth.TokenInput{Name: "Laptop"})
			if err != nil {
				t.Fatal(err)
			}
			if tt.change != nil {
				c := store.Credentials[string(auth.Hash(secret))]
				tt.change(&c, store, a)
				store.Credentials[string(auth.Hash(secret))] = c
			}
			s.SetClock(func() time.Time { return tt.at })
			got, err := s.Authenticate(ctx, secret)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Authenticate() error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.User.ID != a.User.ID || len(got.User.Emails) == 0 || got.Credential.ID != token.ID || got.Session.ID != uuid.Nil {
				t.Errorf("Authenticate() = %+v, want Alice, with her emails, under the token", got)
			}
			if touched := got.Credential.LastUsedAt.Equal(tt.at); touched != tt.wantTouch {
				t.Errorf("last used %v at %v, want touched %t", got.Credential.LastUsedAt, tt.at, tt.wantTouch)
			}
		})
	}
}

func TestTokensAndRevokeToken(t *testing.T) {
	s, store, a := signedInAs(t)
	ctx := context.Background()
	older, _, err := s.CreateToken(ctx, a, auth.TokenInput{Name: "Laptop"})
	if err != nil {
		t.Fatal(err)
	}
	s.SetClock(func() time.Time { return now.Add(time.Minute) })
	newer, _, err := s.CreateToken(ctx, a, auth.TokenInput{Name: "CI"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Tokens(ctx, a.User.ID); err != nil || len(got) != 2 || got[0].ID != newer.ID || got[1].ID != older.ID {
		t.Fatalf("Tokens() = %+v, %v, want the newer first", got, err)
	}
	if err := s.RevokeToken(ctx, uuid.New(), older.ID); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("RevokeToken() of another's token = %v, want auth.ErrNotFound", err)
	}
	if err := s.RevokeToken(ctx, a.User.ID, older.ID); err != nil {
		t.Fatalf("RevokeToken() = %v", err)
	}
	if err := s.RevokeToken(ctx, a.User.ID, older.ID); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("RevokeToken() twice = %v, want auth.ErrNotFound", err)
	}
	if got, err := s.Tokens(ctx, a.User.ID); err != nil || len(got) != 1 || got[0].ID != newer.ID {
		t.Errorf("Tokens() after RevokeToken() = %+v, %v, want the newer alone", got, err)
	}
	store.Fail = errDown
	if err := s.RevokeToken(ctx, a.User.ID, newer.ID); err == nil {
		t.Error("RevokeToken() on a store that fails = nil, want its error")
	}
}
