package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/db/migrations"
)

// TestRepositoryAgentsMigrationDown checks that rolling migration 00075 back deletes a repository's agents, even one that
// acted, keeping the events it acted in as any deleted actor's, and keeps the organization's agents.
func TestRepositoryAgentsMigrationDown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testDatabase(t)
	full := newSchema(t, pool, migrations.FS, migrations.Go...)
	if err := full.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	org := insertOrganizationWithAgents(ctx, t, pool, []string{"Scout"})
	var scout uuid.UUID
	if err := pool.QueryRow(ctx, "SELECT principal_id FROM agents WHERE org_id = $1", org).Scan(&scout); err != nil {
		t.Fatal(err)
	}
	repo, checker := uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO repositories (id, org_id, slug, name) VALUES ($1, $2, 'website', 'website')", []any{repo, org}},
		{"INSERT INTO principals (id, kind, display_name, org_id) VALUES ($1, 'agent', 'checker', $2)", []any{checker, org}},
		{"INSERT INTO agents (principal_id, org_id, name, repo_id, line) VALUES ($1, $2, 'checker', $3, 2)", []any{checker, org, repo}},
		{"INSERT INTO events (actor_id, kind, org_id) VALUES ($1, 'chat.message', $2), ($3, 'chat.message', $2)", []any{checker, org, scout}},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	if _, err := full.provider.DownTo(ctx, 74); err != nil {
		t.Fatalf("DownTo(74) = %v", err)
	}
	tests := []struct {
		name  string
		query string
		id    uuid.UUID
		want  int
	}{
		{name: "the repository's agent is gone", query: "SELECT count(*) FROM principals WHERE id = $1", id: checker, want: 0},
		{name: "the events it acted in stay", query: "SELECT count(*) FROM events WHERE actor_id = $1", id: checker, want: 1},
		{name: "the organization's agent stays", query: "SELECT count(*) FROM agents WHERE principal_id = $1", id: scout, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got int
			if err := pool.QueryRow(ctx, tt.query, tt.id).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("count = %d, want %d", got, tt.want)
			}
		})
	}
}
