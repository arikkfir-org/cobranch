package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

// personalToken is a token of user userID named name, made at `at`.
func personalToken(userID uuid.UUID, name string, at time.Time) auth.Credential {
	return auth.Credential{ID: uuid.New(), Kind: auth.CredentialPersonalToken, PrincipalID: userID, Hint: "abcd", CreatedAt: at, CreatedBy: userID,
		ExpiresAt: at.Add(auth.TokenLifetime), Name: name, Permissions: []string{"repo.push", "repo.read"}, Issuer: "https://idp.example.com", Subject: "1"}
}

func TestAuthStorePersonalTokens(t *testing.T) {
	t.Parallel()
	s, _ := authStore(t)
	ctx := context.Background()
	up := alice(t, s)
	older, newer := personalToken(up.Principal.ID, "Laptop", now), personalToken(up.Principal.ID, "CI", now.Add(time.Minute))
	for i, token := range []auth.Credential{older, newer} {
		if err := s.CreateToken(ctx, token, auth.Hash(token.Name)); err != nil {
			t.Fatalf("CreateToken(%d) error = %v", i, err)
		}
	}
	got, user, err := s.Credential(ctx, auth.Hash("Laptop"))
	if err != nil || got.Kind != auth.CredentialPersonalToken || got.Name != "Laptop" || !slices.Equal(got.Permissions, older.Permissions) ||
		got.Issuer != older.Issuer || got.Subject != older.Subject || !got.ExpiresAt.Equal(older.ExpiresAt) || got.CreatedBy != up.Principal.ID {
		t.Errorf("Credential() = %+v, %v, want the token as made", got, err)
	}
	if user.ID != up.Principal.ID || user.Kind != auth.KindUser || len(user.Emails) != 1 || user.Emails[0].Email != "alice@example.com" {
		t.Errorf("Credential()'s user = %+v, want Alice with her email", user)
	}
	tokens, err := s.Tokens(ctx, up.Principal.ID)
	if err != nil || len(tokens) != 2 || tokens[0].ID != newer.ID || tokens[1].ID != older.ID {
		t.Fatalf("Tokens() = %+v, %v, want the newer first", tokens, err)
	}
	if err := s.RevokeToken(ctx, uuid.New(), older.ID, now.Add(time.Hour)); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("RevokeToken() by another user = %v, want ErrNotFound", err)
	}
	if err := s.RevokeToken(ctx, up.Principal.ID, older.ID, now.Add(time.Hour)); err != nil {
		t.Fatalf("RevokeToken() = %v", err)
	}
	if err := s.RevokeToken(ctx, up.Principal.ID, older.ID, now.Add(time.Hour)); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("RevokeToken() twice = %v, want ErrNotFound", err)
	}
	if got, _, err := s.Credential(ctx, auth.Hash("Laptop")); err != nil || !got.RevokedAt.Equal(now.Add(time.Hour)) {
		t.Errorf("Credential() after RevokeToken() = %+v, %v, want it revoked", got, err)
	}
	if tokens, err := s.Tokens(ctx, up.Principal.ID); err != nil || len(tokens) != 1 || tokens[0].ID != newer.ID {
		t.Errorf("Tokens() after RevokeToken() = %+v, %v, want the newer alone", tokens, err)
	}
	checkTokenRecords(t, s, up.Principal.ID)
}

// checkTokenRecords checks that the tokens' events and audit rows name their user, and that no audit row keeps a hash.
func checkTokenRecords(t *testing.T, s *AuthStore, userID uuid.UUID) {
	t.Helper()
	kinds := eventKinds(t, s.pool)
	if !slices.Equal(kinds[len(kinds)-3:], []string{"personal_token.created", "personal_token.created", "personal_token.revoked"}) {
		t.Errorf("events = %v, want two tokens made and one revoked last", kinds)
	}
	if n := count(t, s.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'credentials' AND principal_id = $1", userID); n != 3 {
		t.Errorf("%d audit rows of the tokens name Alice, want 3: two made, one revoked", n)
	}
	if n := count(t, s.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'credentials' AND (after ? 'token_hash' OR before ? 'token_hash')"); n != 0 {
		t.Errorf("%d audit rows keep a token's hash, want none", n)
	}
}

func TestAuthStorePersonalTokenConstraints(t *testing.T) {
	t.Parallel()
	s, pool := authStore(t)
	ctx := context.Background()
	up := alice(t, s)
	tests := []struct {
		name  string
		query string
	}{
		{name: "a token without an end", query: `INSERT INTO credentials (id, kind, token_hash, principal_id, hint, created_at, name, permissions, issuer, subject)
			VALUES (gen_random_uuid(), 'personal_token', 'h1', $1, 'abcd', now(), 'Laptop', '{repo.read}', 'https://idp.example.com', '1')`},
		{name: "a token without permissions", query: `INSERT INTO credentials (id, kind, token_hash, principal_id, hint, created_at, expires_at, name, issuer, subject)
			VALUES (gen_random_uuid(), 'personal_token', 'h2', $1, 'abcd', now(), now(), 'Laptop', 'https://idp.example.com', '1')`},
		{name: "a token without a name", query: `INSERT INTO credentials (id, kind, token_hash, principal_id, hint, created_at, expires_at, permissions, issuer, subject)
			VALUES (gen_random_uuid(), 'personal_token', 'h3', $1, 'abcd', now(), now(), '{repo.read}', 'https://idp.example.com', '1')`},
		{name: "a token without its session's identity", query: `INSERT INTO credentials (id, kind, token_hash, principal_id, hint, created_at, expires_at, name, permissions)
			VALUES (gen_random_uuid(), 'personal_token', 'h5', $1, 'abcd', now(), now(), 'Laptop', '{repo.read}')`},
		{name: "a token without its session's subject", query: `INSERT INTO credentials (id, kind, token_hash, principal_id, hint, created_at, expires_at, name, permissions, issuer)
			VALUES (gen_random_uuid(), 'personal_token', 'h6', $1, 'abcd', now(), now(), 'Laptop', '{repo.read}', 'https://idp.example.com')`},
		{name: "an API key's secret with permissions", query: `INSERT INTO credentials (id, kind, token_hash, principal_id, hint, created_at, permissions)
			VALUES (gen_random_uuid(), 'api_key', 'h4', $1, 'abcd', now(), '{repo.read}')`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tt.query, up.Principal.ID); err == nil {
				t.Error("the insert succeeded, want the check to refuse it")
			}
		})
	}
}
