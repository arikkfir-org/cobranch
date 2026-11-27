package postgres

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/services/auth"
)

// TestAgentsSeeTheirOrganizationMigration checks that migration 00072 gives Guest at its organization to each agent
// made before it that holds no organization role reading the organization, leaves the others as they are, and that
// running it again gives nothing more; and that its trigger gives Guest to an agent made later without such a role, as
// an older release still rolling out makes one, and nothing to one made with it.
func TestAgentsSeeTheirOrganizationMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testDatabase(t)
	if err := newSchema(t, pool, migrationsBefore(t, "00072")).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	org := insertOrganizationWithAgents(ctx, t, pool, []string{"Scout", "Root", "Watcher", "Reader"})
	agentNamed := func(name string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, "SELECT principal_id FROM agents WHERE org_id = $1 AND name = $2", org, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	grant := func(agent, role uuid.UUID) {
		if _, err := pool.Exec(ctx, "INSERT INTO grants (principal_id, role_id, scope, scope_id, source) VALUES ($1, $2, 'organization', $3, 'grant')", agent, role, org); err != nil {
			t.Fatal(err)
		}
	}
	grant(agentNamed("Root"), auth.RoleMember)
	grant(agentNamed("Watcher"), auth.RoleGuest)
	tests := []struct {
		name  string
		agent string
		want  []uuid.UUID
	}{
		{name: "no roles: Guest", agent: "Scout", want: []uuid.UUID{auth.RoleGuest}},
		{name: "a role that reads the organization: as it was", agent: "Root", want: []uuid.UUID{auth.RoleMember}},
		{name: "Guest already: as it was", agent: "Watcher", want: []uuid.UUID{auth.RoleGuest}},
		{name: "another no roles: Guest", agent: "Reader", want: []uuid.UUID{auth.RoleGuest}},
	}
	full := newSchema(t, pool, migrations.FS, migrations.Go...)
	for run := range 2 {
		if run == 1 {
			if _, err := full.provider.DownTo(ctx, 71); err != nil {
				t.Fatal(err)
			}
		}
		if err := full.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got := rolesAt(ctx, t, pool, agentNamed(tt.agent), org)
				if !slices.Equal(got, tt.want) {
					t.Errorf("run %d: roles at the organization = %v, want %v", run+1, got, tt.want)
				}
			})
		}
	}
	made := []struct {
		name  string
		roles []uuid.UUID
		want  []uuid.UUID
	}{
		{name: "Late", want: []uuid.UUID{auth.RoleGuest}},
		{name: "Lead", roles: []uuid.UUID{auth.RoleMember}, want: []uuid.UUID{auth.RoleMember}},
		{name: "Lurker", roles: []uuid.UUID{auth.RoleGuest}, want: []uuid.UUID{auth.RoleGuest}},
	}
	for _, tt := range made {
		t.Run("made later: "+tt.name, func(t *testing.T) {
			id := makeAgent(ctx, t, pool, org, tt.name, tt.roles)
			if got := rolesAt(ctx, t, pool, id, org); !slices.Equal(got, tt.want) {
				t.Errorf("roles at the organization = %v, want %v", got, tt.want)
			}
		})
	}
}

// makeAgent makes agent name at org with roles there in one transaction, as CreateAgent does, and returns its ID.
func makeAgent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, org uuid.UUID, name string, roles []uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // a no-op once committed
	if _, err := tx.Exec(ctx, "INSERT INTO principals (id, kind, display_name, org_id) VALUES ($1, 'agent', $2, $3)", id, name, org); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO agents (principal_id, org_id, name) VALUES ($1, $2, $3)", id, org, name); err != nil {
		t.Fatal(err)
	}
	for _, role := range roles {
		if _, err := tx.Exec(ctx, "INSERT INTO grants (principal_id, role_id, scope, scope_id, source) VALUES ($1, $2, 'organization', $3, 'grant')", id, role, org); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

// rolesAt returns the roles principal holds at org, ordered by ID.
func rolesAt(ctx context.Context, t *testing.T, pool *pgxpool.Pool, principal, org uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := pool.Query(ctx, "SELECT role_id FROM grants WHERE principal_id = $1 AND scope = 'organization' AND scope_id = $2 ORDER BY role_id", principal, org)
	if err != nil {
		t.Fatal(err)
	}
	var got []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}
