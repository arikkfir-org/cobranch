package postgres

import (
	"context"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/db/migrations"
)

// migrationsBefore are Cobranch's SQL migrations numbered below version, as a database has them before it.
func migrationsBefore(t *testing.T, version string) fstest.MapFS {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, name := range names {
		if strings.SplitN(name, "_", 2)[0] >= version {
			continue
		}
		data, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		before[name] = &fstest.MapFile{Data: data}
	}
	return before
}

// TestAgentsMigrationMovesReservedSlugs checks that migration 00032, which gives the Console the pages agents and
// tasks, moves the repositories that had those slugs to free ones, and keeps their old slugs leading to them.
func TestAgentsMigrationMovesReservedSlugs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testDatabase(t)
	if err := newSchema(t, pool, migrationsBefore(t, "00032")).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// Each repository's slugs before the migration, its current one first.
	ids := insertRepositories(ctx, t, pool, map[string][]string{"agents": {"agents"}, "tasks": {"tasks"}, "taken": {"agents-repository", "tasks-repository"},
		"website": {"website"}})
	if err := newSchema(t, pool, migrations.FS, migrations.Go...).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		repo      string
		wantSlug  string
		wantSlugs []string
	}{
		{repo: "agents", wantSlug: "agents-repository-2", wantSlugs: []string{"agents", "agents-repository-2"}},
		{repo: "tasks", wantSlug: "tasks-repository", wantSlugs: []string{"tasks", "tasks-repository"}},
		{repo: "taken", wantSlug: "agents-repository", wantSlugs: []string{"agents-repository"}},
		{repo: "website", wantSlug: "website", wantSlugs: []string{"website"}},
	} {
		t.Run(tt.repo, func(t *testing.T) {
			var slug string
			if err := pool.QueryRow(ctx, "SELECT slug FROM repositories WHERE id = $1", ids[tt.repo]).Scan(&slug); err != nil || slug != tt.wantSlug {
				t.Errorf("slug = %q, %v; want %q", slug, err, tt.wantSlug)
			}
			if all := repositorySlugs(ctx, t, pool, ids[tt.repo]); !slices.Equal(all, tt.wantSlugs) {
				t.Errorf("slugs = %v, want %v", all, tt.wantSlugs)
			}
		})
	}
}

// TestArtifactsPageMigrationMovesReservedSlugs checks that migration 00056, which gives the Console the page artifacts,
// moves the repositories that had that slug to free ones, and keeps their old slugs leading to them.
func TestArtifactsPageMigrationMovesReservedSlugs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testDatabase(t)
	if err := newSchema(t, pool, migrationsBefore(t, "00056")).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// Each repository's slugs before the migration, its current one first.
	ids := insertRepositories(ctx, t, pool, map[string][]string{"artifacts": {"artifacts", "notes"}, "taken": {"artifacts-repository"},
		"website": {"website"}})
	if err := newSchema(t, pool, migrations.FS, migrations.Go...).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		repo      string
		wantSlug  string
		wantSlugs []string
	}{
		{repo: "artifacts", wantSlug: "artifacts-repository-2", wantSlugs: []string{"artifacts", "artifacts-repository-2", "notes"}},
		{repo: "taken", wantSlug: "artifacts-repository", wantSlugs: []string{"artifacts-repository"}},
		{repo: "website", wantSlug: "website", wantSlugs: []string{"website"}},
	} {
		t.Run(tt.repo, func(t *testing.T) {
			var slug string
			if err := pool.QueryRow(ctx, "SELECT slug FROM repositories WHERE id = $1", ids[tt.repo]).Scan(&slug); err != nil || slug != tt.wantSlug {
				t.Errorf("slug = %q, %v; want %q", slug, err, tt.wantSlug)
			}
			if all := repositorySlugs(ctx, t, pool, ids[tt.repo]); !slices.Equal(all, tt.wantSlugs) {
				t.Errorf("slugs = %v, want %v", all, tt.wantSlugs)
			}
		})
	}
}

// TestSecretsMigrationMovesReservedSlugs checks that migration 00030, which gives the Console the page secrets, moves the
// repositories that had that slug, or issues, which the Issues page took before it, to free ones, and keeps their old
// slugs leading to them.
func TestSecretsMigrationMovesReservedSlugs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testDatabase(t)
	if err := newSchema(t, pool, migrationsBefore(t, "00030")).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// Each repository's slugs before the migration, its current one first.
	ids := insertRepositories(ctx, t, pool, map[string][]string{"secrets": {"secrets"}, "issues": {"issues"}, "taken": {"secrets-repository"},
		"website": {"website"}})
	if err := newSchema(t, pool, migrations.FS, migrations.Go...).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		repo      string
		wantSlug  string
		wantSlugs []string
	}{
		{repo: "secrets", wantSlug: "secrets-repository-2", wantSlugs: []string{"secrets", "secrets-repository-2"}},
		{repo: "issues", wantSlug: "issues-repository", wantSlugs: []string{"issues", "issues-repository"}},
		{repo: "taken", wantSlug: "secrets-repository", wantSlugs: []string{"secrets-repository"}},
		{repo: "website", wantSlug: "website", wantSlugs: []string{"website"}},
	} {
		t.Run(tt.repo, func(t *testing.T) {
			var slug string
			if err := pool.QueryRow(ctx, "SELECT slug FROM repositories WHERE id = $1", ids[tt.repo]).Scan(&slug); err != nil || slug != tt.wantSlug {
				t.Errorf("slug = %q, %v; want %q", slug, err, tt.wantSlug)
			}
			if all := repositorySlugs(ctx, t, pool, ids[tt.repo]); !slices.Equal(all, tt.wantSlugs) {
				t.Errorf("slugs = %v, want %v", all, tt.wantSlugs)
			}
		})
	}
}

// insertRepositories inserts an organization with a repository of each name, which has the slugs slugs names, its
// current one first, and returns their IDs by name.
func insertRepositories(ctx context.Context, t *testing.T, pool *pgxpool.Pool, slugs map[string][]string) map[string]uuid.UUID {
	t.Helper()
	org := uuid.New()
	if _, err := pool.Exec(ctx, "INSERT INTO organizations (id, slug, name, membership_role_id) SELECT $1, 'acme', 'Acme', id FROM roles LIMIT 1", org); err != nil {
		t.Fatal(err)
	}
	ids := map[string]uuid.UUID{}
	for name, own := range slugs {
		ids[name] = uuid.New()
		if _, err := pool.Exec(ctx, "INSERT INTO repositories (id, org_id, slug, name) VALUES ($1, $2, $3, $4)", ids[name], org, own[0], name); err != nil {
			t.Fatal(err)
		}
		for _, slug := range own {
			if _, err := pool.Exec(ctx, "INSERT INTO repository_slugs (org_id, slug, repo_id) VALUES ($1, $2, $3)", org, slug, ids[name]); err != nil {
				t.Fatal(err)
			}
		}
	}
	return ids
}

// repositorySlugs are the slugs that lead to the repository, in order.
func repositorySlugs(ctx context.Context, t *testing.T, pool *pgxpool.Pool, repo uuid.UUID) []string {
	t.Helper()
	rows, err := pool.Query(ctx, "SELECT slug FROM repository_slugs WHERE repo_id = $1 ORDER BY slug", repo)
	if err != nil {
		t.Fatal(err)
	}
	slugs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return slugs
}
