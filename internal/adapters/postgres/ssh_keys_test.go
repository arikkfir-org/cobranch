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

// sshKey is an SSH key of user userID named name, made at `at`.
func sshKey(userID uuid.UUID, name string, at time.Time) auth.Credential {
	return auth.Credential{ID: uuid.New(), Kind: auth.CredentialSSHKey, PrincipalID: userID, Hint: "SHA256:" + name, CreatedAt: at, CreatedBy: userID,
		Name: name, PublicKey: "ssh-ed25519 AAAA" + name}
}

func TestAuthStoreSSHKeys(t *testing.T) {
	t.Parallel()
	s, _ := authStore(t)
	ctx := context.Background()
	up := alice(t, s)
	older, newer := sshKey(up.Principal.ID, "Laptop", now), sshKey(up.Principal.ID, "Desktop", now.Add(time.Minute))
	for i, key := range []auth.Credential{older, newer} {
		if err := s.AddSSHKey(ctx, key, auth.Hash(key.Name)); err != nil {
			t.Fatalf("AddSSHKey(%d) error = %v", i, err)
		}
	}
	got, user, err := s.Credential(ctx, auth.Hash("Laptop"))
	if err != nil || got.Kind != auth.CredentialSSHKey || got.Name != "Laptop" || got.PublicKey != older.PublicKey || got.Hint != older.Hint ||
		!got.ExpiresAt.IsZero() || got.Permissions != nil || got.CreatedBy != up.Principal.ID {
		t.Errorf("Credential() = %+v, %v, want the key as added", got, err)
	}
	if user.ID != up.Principal.ID || user.Kind != auth.KindUser {
		t.Errorf("Credential()'s user = %+v, want Alice", user)
	}
	keys, err := s.SSHKeys(ctx, up.Principal.ID)
	if err != nil || len(keys) != 2 || keys[0].ID != newer.ID || keys[1].ID != older.ID {
		t.Fatalf("SSHKeys() = %+v, %v, want the newer first", keys, err)
	}
	if err := s.DeleteSSHKey(ctx, uuid.New(), older.ID); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("DeleteSSHKey() by another user = %v, want ErrNotFound", err)
	}
	if err := s.DeleteSSHKey(ctx, up.Principal.ID, older.ID); err != nil {
		t.Fatalf("DeleteSSHKey() = %v", err)
	}
	if err := s.DeleteSSHKey(ctx, up.Principal.ID, older.ID); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("DeleteSSHKey() twice = %v, want ErrNotFound", err)
	}
	if _, _, err := s.Credential(ctx, auth.Hash("Laptop")); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("Credential() after DeleteSSHKey() = %v, want ErrNotFound", err)
	}
	if keys, err := s.SSHKeys(ctx, up.Principal.ID); err != nil || len(keys) != 1 || keys[0].ID != newer.ID {
		t.Errorf("SSHKeys() after DeleteSSHKey() = %+v, %v, want the newer alone", keys, err)
	}
	if err := s.AddSSHKey(ctx, sshKey(up.Principal.ID, "Laptop", now.Add(time.Hour)), auth.Hash("Laptop")); err != nil {
		t.Errorf("AddSSHKey() of a deleted key = %v, want it added again", err)
	}
	checkSSHKeyRecords(t, s, up.Principal.ID)
}

// checkSSHKeyRecords checks that the keys' events and audit rows name their user, and that no audit row keeps a hash.
func checkSSHKeyRecords(t *testing.T, s *AuthStore, userID uuid.UUID) {
	t.Helper()
	kinds := eventKinds(t, s.pool)
	if want := []string{"ssh_key.added", "ssh_key.added", "ssh_key.deleted", "ssh_key.added"}; !slices.Equal(kinds[len(kinds)-4:], want) {
		t.Errorf("events = %v, want %v last", kinds, want)
	}
	if n := count(t, s.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'credentials' AND principal_id = $1", userID); n != 4 {
		t.Errorf("%d audit rows of the keys name Alice, want 4: three added, one deleted", n)
	}
	if n := count(t, s.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'credentials' AND (after ? 'token_hash' OR before ? 'token_hash')"); n != 0 {
		t.Errorf("%d audit rows keep a key's hash, want none", n)
	}
}

func TestAuthStoreSSHKeyTaken(t *testing.T) {
	t.Parallel()
	s, _ := authStore(t)
	ctx := context.Background()
	up, other := alice(t, s), signUp("https://idp.example.com", "2", "bob@example.com", true)
	if err := s.SignUp(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSSHKey(ctx, sshKey(up.Principal.ID, "Laptop", now), auth.Hash("key")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		userID uuid.UUID
	}{
		{name: "the same user", userID: up.Principal.ID},
		{name: "another user", userID: other.Principal.ID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.AddSSHKey(ctx, sshKey(tt.userID, "Again", now), auth.Hash("key")); !errors.Is(err, auth.ErrSSHKeyTaken) {
				t.Errorf("AddSSHKey() of a key held = %v, want ErrSSHKeyTaken", err)
			}
		})
	}
}

func TestAuthStoreSSHKeyConstraints(t *testing.T) {
	t.Parallel()
	s, pool := authStore(t)
	ctx := context.Background()
	up := alice(t, s)
	tests := []struct {
		name  string
		query string
	}{
		{name: "a key without its public key", query: `INSERT INTO credentials (id, kind, token_hash, principal_id, hint, created_at, name)
			VALUES (gen_random_uuid(), 'ssh_key', 'h1', $1, 'SHA256:x', now(), 'Laptop')`},
		{name: "a key without a name", query: `INSERT INTO credentials (id, kind, token_hash, principal_id, hint, created_at, public_key)
			VALUES (gen_random_uuid(), 'ssh_key', 'h2', $1, 'SHA256:x', now(), 'ssh-ed25519 AAAA')`},
		{name: "a key that expires", query: `INSERT INTO credentials (id, kind, token_hash, principal_id, hint, created_at, expires_at, name, public_key)
			VALUES (gen_random_uuid(), 'ssh_key', 'h3', $1, 'SHA256:x', now(), now(), 'Laptop', 'ssh-ed25519 AAAA')`},
		{name: "a key with permissions", query: `INSERT INTO credentials (id, kind, token_hash, principal_id, hint, created_at, name, permissions, public_key)
			VALUES (gen_random_uuid(), 'ssh_key', 'h4', $1, 'SHA256:x', now(), 'Laptop', '{repo.read}', 'ssh-ed25519 AAAA')`},
		{name: "a token with a public key", query: `INSERT INTO credentials (id, kind, token_hash, principal_id, hint, created_at, expires_at, name, permissions, issuer, subject, public_key)
			VALUES (gen_random_uuid(), 'personal_token', 'h5', $1, 'abcd', now(), now(), 'Laptop', '{repo.read}', 'https://idp.example.com', '1', 'ssh-ed25519 AAAA')`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tt.query, up.Principal.ID); err == nil {
				t.Error("the insert succeeded, want the check to refuse it")
			}
		})
	}
}
