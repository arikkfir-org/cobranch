package postgres

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/services/auth"
)

// TestBuiltInRoles holds migration 2's roles to the principals design: every permission is one of the catalog's, at
// the role's scope, the Owner holds every organization and repository permission, and the Maintainer every
// repository one.
func TestBuiltInRoles(t *testing.T) {
	t.Parallel()
	_, pool := authStore(t)
	type role struct {
		ID          uuid.UUID
		Scope       string
		Name        string
		Permissions []string
	}
	rows, err := pool.Query(context.Background(), "SELECT id, scope, name, permissions FROM roles WHERE built_in ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	roles, err := pgx.CollectRows(rows, pgx.RowToStructByName[role])
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 9 {
		t.Fatalf("got %d built-in roles, want 9", len(roles))
	}
	allowed := map[string][]string{
		"installation": auth.InstallationPermissions,
		"organization": slices.Concat(auth.OrganizationPermissions, auth.RepositoryPermissions),
		"repository":   auth.RepositoryPermissions,
	}
	everything := map[string][]string{
		"Owner":      allowed["organization"],
		"Maintainer": auth.RepositoryPermissions,
	}
	for _, r := range roles {
		for _, p := range r.Permissions {
			if !slices.Contains(allowed[r.Scope], p) {
				t.Errorf("%s holds %q, not a %s permission of the catalog", r.Name, p, r.Scope)
			}
		}
		for _, p := range everything[r.Name] {
			if !slices.Contains(r.Permissions, p) {
				t.Errorf("%s lacks %q", r.Name, p)
			}
		}
	}
	if roles[0].ID != auth.RoleInstallationAdmin || roles[2].Name != "Owner" {
		t.Errorf("roles by id: got %v", roles)
	}
}
