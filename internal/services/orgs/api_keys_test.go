package orgs_test

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

var errDown = errors.New("down")

// third is the error of a call that also returns two values.
func third[T, U any](_ T, _ U, err error) error { return err }

// keeper is a custom role of Acme that manages principals but not grants, so a key's roles are beyond it.
var keeper = orgs.Role{ID: uuid.MustParse("00000000-0000-7000-8000-000000000f03"), Name: "Keeper", Permissions: []string{"org.read", "org.principals.manage"}}

// withKeeper adds Keeper to Acme's roles and makes Gwen hold it, beside Guest.
func (f *fixture) withKeeper() {
	role := keeper
	role.OrgID = f.acme.ID
	f.store.RoleList = append(f.store.RoleList, role)
	f.store.Grant(f.gwen.ID, role.ID, auth.ScopeOrganization, f.acme.ID)
}

// deployKey makes Acme's key "Deploy" holding role as Olivia, and returns it with its secret.
func (f *fixture) deployKey(ctx context.Context, t *testing.T, role uuid.UUID) (orgs.APIKey, string) {
	t.Helper()
	key, secret, err := f.service.CreateAPIKey(ctx, f.olivia, f.acme.ID, orgs.APIKeyInput{Name: "Deploy", RoleIDs: []uuid.UUID{role}})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	return key, secret
}

// TestAPIKeyLifecycle makes, rotates and deletes a key as an Owner.
func TestAPIKeyLifecycle(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	key, secret := f.deployKey(ctx, t, auth.RoleMember)
	if !auth.ValidSecret(auth.PrefixAPIKey, secret) || key.Name != "Deploy" || key.OrgID != f.acme.ID || key.CreatedBy != f.olivia.ID ||
		len(key.Roles) != 1 || key.Roles[0].ID != auth.RoleMember || len(key.Secrets) != 1 || key.Secrets[0].Hint != auth.Hint(secret) ||
		!key.Secrets[0].ExpiresAt.Equal(now.Add(orgs.APIKeyLifetime)) {
		t.Fatalf("CreateAPIKey() = %+v, %q", key, secret)
	}
	if !slices.Equal(f.store.Hashes[key.Secrets[0].ID], auth.Hash(secret)) {
		t.Error("the store keeps something other than the secret's hash")
	}
	if held, _ := f.store.Permissions(ctx, key.ID, auth.ScopeOrganization, f.acme.ID); !slices.Contains(held, "org.read") {
		t.Errorf("the key holds %v at Acme, want Member's permissions", held)
	}
	keys, err := f.service.APIKeys(ctx, f.gwen, f.acme.ID)
	if err != nil || len(keys) != 1 || keys[0].ID != key.ID {
		t.Errorf("APIKeys() as a Guest = %+v, %v, want the key", keys, err)
	}
	if _, err := f.service.APIKeys(ctx, f.nia, f.acme.ID); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("APIKeys() from outside: error = %v, want not found", err)
	}
	rotated, second, err := f.service.RotateAPIKey(ctx, f.olivia, f.acme.ID, key.ID, orgs.Expiry{Never: true})
	if err != nil || len(rotated.Secrets) != 2 || !rotated.Secrets[1].ExpiresAt.IsZero() || second == secret || !auth.ValidSecret(auth.PrefixAPIKey, second) {
		t.Fatalf("RotateAPIKey() = %+v, %q, %v", rotated, second, err)
	}
	if _, _, err := f.service.RotateAPIKey(ctx, f.olivia, f.acme.ID, key.ID, orgs.Expiry{}); !errors.Is(err, orgs.ErrTwoSecrets) {
		t.Errorf("a third secret: error = %v, want two secrets", err)
	}
	if err := f.service.RevokeSecret(ctx, f.olivia, f.acme.ID, key.ID, key.Secrets[0].ID); err != nil {
		t.Errorf("RevokeSecret() error = %v", err)
	}
	if err := f.service.RevokeSecret(ctx, f.olivia, f.acme.ID, key.ID, key.Secrets[0].ID); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("RevokeSecret() again: error = %v, want not found", err)
	}
	if _, _, err := f.service.RotateAPIKey(ctx, f.olivia, f.acme.ID, key.ID, orgs.Expiry{}); err != nil {
		t.Errorf("RotateAPIKey() after a revocation: error = %v", err)
	}
	if err := f.service.DeleteAPIKey(ctx, f.olivia, f.acme.ID, key.ID); err != nil {
		t.Errorf("DeleteAPIKey() error = %v", err)
	}
	if held, _ := f.store.Permissions(ctx, key.ID, auth.ScopeOrganization, f.acme.ID); len(held) != 0 {
		t.Errorf("the deleted key still holds %v", held)
	}
	if _, err := f.service.APIKeys(ctx, f.olivia, f.acme.ID); err != nil {
		t.Error(err)
	} else if len(f.store.Keys) != 0 {
		t.Error("the key remains")
	}
	want := []string{"api_key.created", "api_key.rotated", "api_key.secret_revoked", "api_key.rotated", "api_key.deleted"}
	if !slices.Equal(f.store.Events, want) {
		t.Errorf("events = %v, want %v", f.store.Events, want)
	}
}

func TestMakingAPIKeys(t *testing.T) {
	member := []uuid.UUID{auth.RoleMember}
	cases := []testCase{
		{name: "a Member", run: func(ctx context.Context, f *fixture) error {
			return third(f.service.CreateAPIKey(ctx, f.max, f.acme.ID, orgs.APIKeyInput{Name: "Deploy", RoleIDs: member}))
		}, wantErr: auth.ErrForbidden},
		{name: "a principal manager without org.grants.manage", run: func(ctx context.Context, f *fixture) error {
			f.withKeeper()
			return third(f.service.CreateAPIKey(ctx, f.gwen, f.acme.ID, orgs.APIKeyInput{Name: "Deploy", RoleIDs: []uuid.UUID{auth.RoleGuest}}))
		}, wantErr: auth.ErrForbidden},
		{name: "a granter who lacks the role's permissions", run: func(ctx context.Context, f *fixture) error {
			f.withGranter()
			f.store.Grant(f.gwen.ID, keeper.ID, auth.ScopeOrganization, f.acme.ID)
			f.store.RoleList = append(f.store.RoleList, keeper)
			return third(f.service.CreateAPIKey(ctx, f.gwen, f.acme.ID, orgs.APIKeyInput{Name: "Deploy", RoleIDs: member}))
		}, wantErr: auth.ErrForbidden},
		{name: "a granter with the role's permissions", run: func(ctx context.Context, f *fixture) error {
			f.withGranter()
			f.store.Grant(f.gwen.ID, keeper.ID, auth.ScopeOrganization, f.acme.ID)
			f.store.RoleList = append(f.store.RoleList, keeper)
			return third(f.service.CreateAPIKey(ctx, f.gwen, f.acme.ID, orgs.APIKeyInput{Name: "Deploy", RoleIDs: []uuid.UUID{auth.RoleGuest}}))
		}, wantEvents: []string{"api_key.created"}},
		{name: "a key holding Owner", run: func(ctx context.Context, f *fixture) error {
			key, _ := f.deployKey(ctx, t, auth.RoleOwner)
			asKey := orgs.Viewer{Principal: auth.Principal{ID: key.ID, Kind: auth.KindAPIKey, DisplayName: key.Name}}
			return third(f.service.CreateAPIKey(ctx, asKey, f.acme.ID, orgs.APIKeyInput{Name: "Other", RoleIDs: member}))
		}, wantErr: auth.ErrForbidden, wantEvents: []string{"api_key.created"}},
		{name: "from outside", run: func(ctx context.Context, f *fixture) error {
			return third(f.service.CreateAPIKey(ctx, f.nia, f.acme.ID, orgs.APIKeyInput{Name: "Deploy", RoleIDs: member}))
		}, wantErr: orgs.ErrNotFound},
		{name: "a role that isn't", run: func(ctx context.Context, f *fixture) error {
			return third(f.service.CreateAPIKey(ctx, f.olivia, f.acme.ID, orgs.APIKeyInput{Name: "Deploy", RoleIDs: []uuid.UUID{uuid.New()}}))
		}, wantCode: "role.invalid"},
		{name: "an installation role", run: func(ctx context.Context, f *fixture) error {
			return third(f.service.CreateAPIKey(ctx, f.olivia, f.acme.ID, orgs.APIKeyInput{Name: "Deploy", RoleIDs: []uuid.UUID{auth.RoleInstallationAdmin}}))
		}, wantCode: "role.invalid"},
	}
	runCases(t, cases)
}

// TestAPIKeyInputs checks what makes a key: its name, roles and first secret's end.
func TestAPIKeyInputs(t *testing.T) {
	member := []uuid.UUID{auth.RoleMember}
	cases := []testCase{
		{name: "no name", run: func(ctx context.Context, f *fixture) error {
			return third(f.service.CreateAPIKey(ctx, f.olivia, f.acme.ID, orgs.APIKeyInput{Name: "  ", RoleIDs: member}))
		}, wantCode: "api_key.name_invalid"},
		{name: "no roles", run: func(ctx context.Context, f *fixture) error {
			return third(f.service.CreateAPIKey(ctx, f.olivia, f.acme.ID, orgs.APIKeyInput{Name: "Deploy"}))
		}, wantCode: "api_key.roles_invalid"},
		{name: "an end that passed", run: func(ctx context.Context, f *fixture) error {
			return third(f.service.CreateAPIKey(ctx, f.olivia, f.acme.ID, orgs.APIKeyInput{Name: "Deploy", RoleIDs: member, Expiry: orgs.Expiry{At: now}}))
		}, wantCode: "api_key.expiry_invalid"},
		{name: "never, with an end", run: func(ctx context.Context, f *fixture) error {
			return third(f.service.CreateAPIKey(ctx, f.olivia, f.acme.ID, orgs.APIKeyInput{Name: "Deploy", RoleIDs: member, Expiry: orgs.Expiry{At: now.Add(time.Hour), Never: true}}))
		}, wantCode: "api_key.expiry_invalid"},
		{name: "an end", run: func(ctx context.Context, f *fixture) error {
			key, _, err := f.service.CreateAPIKey(ctx, f.olivia, f.acme.ID, orgs.APIKeyInput{Name: "Deploy", RoleIDs: member, Expiry: orgs.Expiry{At: now.Add(time.Hour)}})
			if err == nil && !key.Secrets[0].ExpiresAt.Equal(now.Add(time.Hour)) {
				return errors.New("the end wasn't kept")
			}
			return got(err)
		}, wantEvents: []string{"api_key.created"}},
		{name: "the same role twice", run: func(ctx context.Context, f *fixture) error {
			key, _, err := f.service.CreateAPIKey(ctx, f.olivia, f.acme.ID, orgs.APIKeyInput{Name: "Deploy", RoleIDs: []uuid.UUID{auth.RoleMember, auth.RoleMember}})
			if err == nil && len(key.Roles) != 1 {
				return errors.New("the role was kept twice")
			}
			return got(err)
		}, wantEvents: []string{"api_key.created"}},
		{name: "a store that fails", run: func(ctx context.Context, f *fixture) error {
			f.store.Fail = errDown
			return third(f.service.CreateAPIKey(ctx, f.olivia, f.acme.ID, orgs.APIKeyInput{Name: "Deploy", RoleIDs: member}))
		}, wantErr: errDown},
	}
	runCases(t, cases)
}

func TestChangingAPIKeys(t *testing.T) {
	cases := []testCase{
		{name: "rotate as a Member", run: func(ctx context.Context, f *fixture) error {
			key, _ := f.deployKey(ctx, t, auth.RoleMember)
			f.store.Events = nil
			return third(f.service.RotateAPIKey(ctx, f.max, f.acme.ID, key.ID, orgs.Expiry{}))
		}, wantErr: auth.ErrForbidden},
		{name: "rotate as a principal manager", run: func(ctx context.Context, f *fixture) error {
			f.withKeeper()
			key, _ := f.deployKey(ctx, t, auth.RoleMember)
			f.store.Events = nil
			return third(f.service.RotateAPIKey(ctx, f.gwen, f.acme.ID, key.ID, orgs.Expiry{}))
		}, wantEvents: []string{"api_key.rotated"}},
		{name: "rotate a key that isn't", run: func(ctx context.Context, f *fixture) error {
			return third(f.service.RotateAPIKey(ctx, f.olivia, f.acme.ID, uuid.New(), orgs.Expiry{}))
		}, wantErr: orgs.ErrNotFound},
		{name: "rotate another organization's key", run: func(ctx context.Context, f *fixture) error {
			key, _ := f.deployKey(ctx, t, auth.RoleMember)
			f.store.Events = nil
			return third(f.service.RotateAPIKey(ctx, f.max, f.globex.ID, key.ID, orgs.Expiry{}))
		}, wantErr: orgs.ErrNotFound},
		{name: "rotate to an end that passed", run: func(ctx context.Context, f *fixture) error {
			key, _ := f.deployKey(ctx, t, auth.RoleMember)
			f.store.Events = nil
			return third(f.service.RotateAPIKey(ctx, f.olivia, f.acme.ID, key.ID, orgs.Expiry{At: now.Add(-time.Second)}))
		}, wantCode: "api_key.expiry_invalid"},
		{name: "revoke as a principal manager", run: func(ctx context.Context, f *fixture) error {
			f.withKeeper()
			key, _ := f.deployKey(ctx, t, auth.RoleMember)
			f.store.Events = nil
			return f.service.RevokeSecret(ctx, f.gwen, f.acme.ID, key.ID, key.Secrets[0].ID)
		}, wantEvents: []string{"api_key.secret_revoked"}},
		{name: "revoke a secret that isn't", run: func(ctx context.Context, f *fixture) error {
			key, _ := f.deployKey(ctx, t, auth.RoleMember)
			f.store.Events = nil
			return f.service.RevokeSecret(ctx, f.olivia, f.acme.ID, key.ID, uuid.New())
		}, wantErr: orgs.ErrNotFound},
		{name: "delete a key whose roles the manager covers", run: func(ctx context.Context, f *fixture) error {
			f.withKeeper()
			key, _ := f.deployKey(ctx, t, auth.RoleGuest)
			f.store.Events = nil
			return f.service.DeleteAPIKey(ctx, f.gwen, f.acme.ID, key.ID)
		}, wantEvents: []string{"api_key.deleted"}},
		{name: "delete a key whose roles the manager lacks", run: func(ctx context.Context, f *fixture) error {
			f.withKeeper()
			key, _ := f.deployKey(ctx, t, auth.RoleOwner)
			f.store.Events = nil
			return f.service.DeleteAPIKey(ctx, f.gwen, f.acme.ID, key.ID)
		}, wantErr: auth.ErrForbidden},
		{name: "delete as a Member", run: func(ctx context.Context, f *fixture) error {
			key, _ := f.deployKey(ctx, t, auth.RoleMember)
			f.store.Events = nil
			return f.service.DeleteAPIKey(ctx, f.max, f.acme.ID, key.ID)
		}, wantErr: auth.ErrForbidden},
		{name: "delete a key that isn't", run: func(ctx context.Context, f *fixture) error {
			return f.service.DeleteAPIKey(ctx, f.olivia, f.acme.ID, uuid.New())
		}, wantErr: orgs.ErrNotFound},
	}
	runCases(t, cases)
}
