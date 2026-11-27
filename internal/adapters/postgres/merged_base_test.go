package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/db/migrations"
)

// mergedBranch is a branch as migration 00067 finds it: merged at head at when, deleted since or not, by a push whose
// row names it by its ID or, as pushes did before they kept their branches' IDs, by its name alone.
type mergedBranch struct {
	name    string
	head    string
	when    time.Time
	deleted bool
	byName  bool
	// oldTip is the default branch's tip before the push that merged it; "" for a merge whose push is gone.
	oldTip string
}

// insertMergedBranch writes b's row into repo and, for a merge whose push is still there, the push that made it.
func insertMergedBranch(ctx context.Context, t *testing.T, pool *pgxpool.Pool, org, repo uuid.UUID, b mergedBranch) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var deletedAt *time.Time
	if b.deleted {
		deletedAt = &b.when
	}
	if _, err := pool.Exec(ctx, `INSERT INTO branches (id, org_id, repo_id, name, head, created_at, updated_at, deleted_at, merged_head, merged_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6, $7, $5, $6)`, id, org, repo, b.name, b.head, b.when, deletedAt); err != nil {
		t.Fatal(err)
	}
	if b.oldTip == "" {
		return id
	}
	merged := `[{"branch": "` + b.name + `", "sha": "` + b.head + `", "id": "` + id.String() + `"}]`
	if b.byName {
		merged = `[{"branch": "` + b.name + `", "sha": "` + b.head + `"}]`
	}
	updates := `[{"name": "refs/heads/other", "old": "` + strings.Repeat("9", 40) + `", "new": "` + b.head + `"},
		{"name": "refs/heads/main", "old": "` + b.oldTip + `", "new": "` + b.head + `"}]`
	if _, err := pool.Exec(ctx, `INSERT INTO pushes (id, org_id, repo_id, transport, ref_updates, received_at, committed_at, merged)
		VALUES ($1, $2, $3, 'https', $4, $5, $5, $6)`, uuid.New(), org, repo, updates, b.when, merged); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestMergedBaseMigrationBackfills checks that migration 00067 gives each merge recorded before it the default
// branch's tip from before the push that made it: by the branch's ID or, for an older push, its live branch's name,
// for a deleted branch too, taking the tip of the push recorded when the merge was, and none where no push remains.
func TestMergedBaseMigrationBackfills(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testDatabase(t)
	if err := newSchema(t, pool, migrationsBefore(t, "00067")).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := insertRepositories(ctx, t, pool, map[string][]string{"website": {"website"}})["website"]
	var org uuid.UUID
	if err := pool.QueryRow(ctx, "SELECT org_id FROM repositories WHERE id = $1", repo).Scan(&org); err != nil {
		t.Fatal(err)
	}
	sha := func(c string) string { return strings.Repeat(c, 40) }
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		mergedBranch
		want string
	}{
		{name: "by its ID", mergedBranch: mergedBranch{name: "feature", head: sha("b"), when: at, oldTip: sha("a")}, want: sha("a")},
		{name: "deleted since, by its ID", mergedBranch: mergedBranch{name: "gone", head: sha("c"), when: at.Add(time.Hour), deleted: true,
			oldTip: sha("b")}, want: sha("b")},
		{name: "by its name, from a push before pushes kept IDs", mergedBranch: mergedBranch{name: "old", head: sha("d"),
			when: at.Add(2 * time.Hour), byName: true, oldTip: sha("c")}, want: sha("c")},
		{name: "whose push is gone", mergedBranch: mergedBranch{name: "orphan", head: sha("e"), when: at.Add(3 * time.Hour)}},
	}
	ids := make([]uuid.UUID, len(tests))
	for i, tt := range tests {
		ids[i] = insertMergedBranch(ctx, t, pool, org, repo, tt.mergedBranch)
	}
	// An earlier push that merged the same head of feature, recorded at another time, isn't the merge recorded.
	if _, err := pool.Exec(ctx, `INSERT INTO pushes (id, org_id, repo_id, transport, ref_updates, received_at, committed_at, merged)
		VALUES ($1, $2, $3, 'https', $4, $5, $5, $6)`, uuid.New(), org, repo,
		`[{"name": "refs/heads/main", "old": "`+sha("f")+`", "new": "`+sha("b")+`"}]`, at.Add(-time.Hour),
		`[{"branch": "feature", "sha": "`+sha("b")+`", "id": "`+ids[0].String()+`"}]`); err != nil {
		t.Fatal(err)
	}
	if err := newSchema(t, pool, migrations.FS, migrations.Go...).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got *string
			if err := pool.QueryRow(ctx, "SELECT merged_base FROM branches WHERE id = $1", ids[i]).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if derefString(got) != tt.want {
				t.Errorf("merged_base = %q, want %q", derefString(got), tt.want)
			}
		})
	}
}
