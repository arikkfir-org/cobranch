package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/services/auth"
)

// TestAgentsSeeTheirOrganizationMigration checks that migration 00072 gives Guest at its organization to each agent
// made before it that holds no organization role reading the organization, leaves the others as they are, and that
// running it again gives nothing more.
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
				rows, err := pool.Query(ctx, "SELECT role_id FROM grants WHERE principal_id = $1 AND scope = 'organization' AND scope_id = $2 ORDER BY role_id", agentNamed(tt.agent), org)
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
				if len(got) != len(tt.want) || got[0] != tt.want[0] {
					t.Errorf("run %d: roles at the organization = %v, want %v", run+1, got, tt.want)
				}
			})
		}
	}
}
