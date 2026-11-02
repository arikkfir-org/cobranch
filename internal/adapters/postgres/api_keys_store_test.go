package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// deployKey makes Acme's key holding Member as Olivia, with a secret that expires in an hour, and returns the key
// and the secret's hash.
func deployKey(t *testing.T, f *orgFixture) (orgs.APIKey, []byte) {
	t.Helper()
	token := auth.NewSecret(auth.PrefixAPIKey)
	secret := orgs.Secret{ID: uuid.New(), Hint: auth.Hint(token), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	key := orgs.APIKey{ID: uuid.New(), OrgID: f.acme.ID, Name: "Deploy", CreatedBy: f.olivia, CreatedAt: now, Roles: []orgs.Role{{ID: auth.RoleMember}}}
	if err := f.s.CreateAPIKey(context.Background(), f.olivia, key, secret, auth.Hash(token)); err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	return key, auth.Hash(token)
}

func TestOrgStoreAPIKeys(t *testing.T) {
	t.Parallel()
	f := newOrgFixture(t)
	ctx := context.Background()
	key, hash := deployKey(t, f)
	keys, err := f.s.APIKeys(ctx, f.acme.ID)
	if err != nil || len(keys) != 1 || keys[0].Name != "Deploy" || keys[0].CreatedBy != f.olivia || len(keys[0].Roles) != 1 ||
		keys[0].Roles[0].Name != "Member" || len(keys[0].Secrets) != 1 || !keys[0].Secrets[0].ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("APIKeys() = %+v, %v, want Deploy holding Member with one secret", keys, err)
	}
	if held, _ := f.s.Permissions(ctx, key.ID, auth.ScopeOrganization, f.acme.ID); !slices.Contains(held, "org.read") {
		t.Errorf("the key holds %v, want Member's permissions", held)
	}
	auths := NewAuthStore(f.pool)
	cred, principal, err := auths.Credential(ctx, hash)
	if err != nil || cred.Kind != auth.CredentialAPIKey || cred.PrincipalID != key.ID || cred.Hint != keys[0].Secrets[0].Hint || principal.Kind != auth.KindAPIKey || principal.DisplayName != "Deploy" {
		t.Errorf("Credential() = %+v, %+v, %v", cred, principal, err)
	}
	if err := auths.TouchCredential(ctx, cred.ID, now.Add(time.Minute)); err != nil {
		t.Error(err)
	}
	if got, err := f.s.APIKey(ctx, f.acme.ID, key.ID); err != nil || !got.Secrets[0].LastUsedAt.Equal(now.Add(time.Minute)) {
		t.Errorf("APIKey() after a touch = %+v, %v", got, err)
	}
	if _, err := f.s.APIKey(ctx, uuid.New(), key.ID); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("APIKey() at another organization: error = %v, want not found", err)
	}
	testSecrets(t, f, key)
}

// testSecrets rotates the key's secrets, two at most, then deletes the key.
func testSecrets(t *testing.T, f *orgFixture, key orgs.APIKey) {
	t.Helper()
	ctx := context.Background()
	add := func(at time.Time) error {
		token := auth.NewSecret(auth.PrefixAPIKey)
		return f.s.AddSecret(ctx, f.olivia, f.acme.ID, key.ID, orgs.Secret{ID: uuid.New(), Hint: auth.Hint(token), CreatedAt: at}, auth.Hash(token), at)
	}
	if err := add(now.Add(time.Minute)); err != nil {
		t.Fatalf("AddSecret() error = %v", err)
	}
	if err := add(now.Add(2 * time.Minute)); !errors.Is(err, orgs.ErrTwoSecrets) {
		t.Errorf("a third secret: error = %v, want two secrets", err)
	}
	if err := add(now.Add(2 * time.Hour)); err != nil {
		t.Errorf("a third secret once the first expired: error = %v", err)
	}
	got, err := f.s.APIKey(ctx, f.acme.ID, key.ID)
	if err != nil || len(got.Secrets) != 3 {
		t.Fatalf("APIKey() = %+v, %v, want three secrets", got, err)
	}
	if err := f.s.RevokeSecret(ctx, f.olivia, f.acme.ID, key.ID, got.Secrets[1].ID, now.Add(3*time.Hour)); err != nil {
		t.Errorf("RevokeSecret() error = %v", err)
	}
	if err := f.s.RevokeSecret(ctx, f.olivia, f.acme.ID, key.ID, got.Secrets[1].ID, now.Add(3*time.Hour)); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("RevokeSecret() again: error = %v, want not found", err)
	}
	if err := f.s.AddSecret(ctx, f.olivia, uuid.New(), key.ID, orgs.Secret{ID: uuid.New()}, []byte("x"), now); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("AddSecret() at another organization: error = %v, want not found", err)
	}
	inv := orgs.Invitation{ID: uuid.New(), OrgID: f.acme.ID, Email: "late@example.com", RoleID: auth.RoleMember, InvitedBy: key.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := f.s.Invite(ctx, key.ID, inv); err != nil {
		t.Fatalf("Invite() as the key: error = %v", err)
	}
	if err := f.s.DeleteAPIKey(ctx, f.olivia, f.acme.ID, key.ID); err != nil {
		t.Fatalf("DeleteAPIKey() error = %v", err)
	}
	if err := f.s.DeleteAPIKey(ctx, f.olivia, f.acme.ID, key.ID); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("DeleteAPIKey() again: error = %v, want not found", err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM credentials WHERE principal_id = $1", key.ID); n != 0 {
		t.Errorf("%d secrets survive the key", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM grants WHERE principal_id = $1", key.ID); n != 0 {
		t.Errorf("%d grants survive the key", n)
	}
	if got, err := f.s.Invitation(ctx, inv.ID); err != nil || got.InvitedBy != uuid.Nil {
		t.Errorf("the deleted key's invitation = %+v, %v, want it kept with no inviter", got, err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE actor_id = $1", key.ID); n != 1 {
		t.Errorf("%d events name the deleted key as actor, want its invitation's", n)
	}
	want := []string{"api_key.created", "api_key.rotated", "api_key.rotated", "api_key.secret_revoked", "invitation.made", "api_key.deleted"}
	if got := eventKinds(t, f.pool); len(got) < len(want) || !slices.Equal(got[len(got)-len(want):], want) {
		t.Errorf("events = %v, want them to end with %v", got, want)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'credentials' AND principal_id = $1", f.olivia); n != 7 {
		t.Errorf("%d audit rows on credentials name Olivia, want 7: three inserts, a revocation and three deletes, not the touch", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'credentials' AND after ? 'token_hash' OR before ? 'token_hash'"); n != 0 {
		t.Errorf("%d audit rows carry a token hash", n)
	}
}

// TestOrgStoreRotateAtOnce rotates a key with one live secret twice at once: exactly one rotation makes the second.
func TestOrgStoreRotateAtOnce(t *testing.T) {
	t.Parallel()
	f := newOrgFixture(t)
	ctx := context.Background()
	key, _ := deployKey(t, f)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			token := auth.NewSecret(auth.PrefixAPIKey)
			errs <- f.s.AddSecret(ctx, f.olivia, f.acme.ID, key.ID, orgs.Secret{ID: uuid.New(), Hint: auth.Hint(token), CreatedAt: now}, auth.Hash(token), now)
		}()
	}
	var added, refused int
	for range 2 {
		switch err := <-errs; {
		case err == nil:
			added++
		case errors.Is(err, orgs.ErrTwoSecrets):
			refused++
		default:
			t.Errorf("AddSecret() error = %v", err)
		}
	}
	if added != 1 || refused != 1 {
		t.Errorf("added %d and refused %d, want one of each", added, refused)
	}
}
