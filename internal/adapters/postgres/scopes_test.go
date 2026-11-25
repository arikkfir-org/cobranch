package postgres

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/db/migrations"
	"cobranch.dev/db/seeds"
	"cobranch.dev/internal/adapters/postgres/postgrestest"
	"cobranch.dev/internal/adapters/postgres/queries"
)

// tenantRole is the role a pinned query runs as.
const tenantRole = "cobranch_tenant"

// isolatedDatabase is a migrated database with Cobranch's seeds, which hold two organizations' rows.
func isolatedDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := testDatabase(t)
	if err := newSchema(t, pool, migrations.FS, migrations.Go...).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	seed(t, pool, seeds.Go)
	return pool
}

// pinned is the role and the organization a query on conn runs with.
func pinned(ctx context.Context, conn queries.DBTX) (role, org string, err error) {
	if err := conn.QueryRow(ctx, "SELECT current_user::text, coalesce(current_setting('cobranch.org_id', true), '')").Scan(&role, &org); err != nil {
		return "", "", fmt.Errorf("reading the role and the organization: %w", err)
	}
	return role, org, nil
}

// scopesFixture is a seeded database with its two organizations and one of Globex's repositories.
func scopesFixture(t *testing.T) (db *DB, acme, globex, repo uuid.UUID) {
	t.Helper()
	pool := isolatedDatabase(t)
	if err := pool.QueryRow(context.Background(), `SELECT (SELECT id FROM organizations WHERE slug = 'acme'), o.id, r.id
		FROM organizations o JOIN repositories r ON r.org_id = o.id WHERE o.slug = 'globex' LIMIT 1`).Scan(&acme, &globex, &repo); err != nil {
		t.Fatal(err)
	}
	return newDB(pool), acme, globex, repo
}

// TestScopesPin checks the role and the organization each scope's queries run with, alone and in a transaction, and
// that a pooled connection keeps nothing of the scope that last used it.
func TestScopesPin(t *testing.T) {
	t.Parallel()
	db, acme, globex, repo := scopesFixture(t)
	ctx := context.Background()
	owner, _, err := pinned(ctx, db.pool)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		scope    Scope
		wantRole string
		wantOrg  string
	}{
		{"an organization", db.In(acme), tenantRole, acme.String()},
		{"a row's organization", db.Of(Repositories, repo), tenantRole, globex.String()},
		{"several rows' organization", db.OfAny(Repositories, []uuid.UUID{repo}), tenantRole, globex.String()},
		{"a row that isn't there", db.Of(Repositories, uuid.New()), tenantRole, uuid.Nil.String()},
		{"no rows", db.OfAny(Repositories, nil), tenantRole, uuid.Nil.String()},
		{"everywhere", db.Everywhere("a test"), owner, ""},
		{"deferred", db.Deferred(), owner, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role, org, err := pinned(ctx, scopedConn{tt.scope})
			if err != nil || role != tt.wantRole || org != tt.wantOrg {
				t.Errorf("a query alone runs as %q in %q (%v), want %q in %q", role, org, err, tt.wantRole, tt.wantOrg)
			}
			err = tt.scope.Transact(ctx, "a test", uuid.Nil, func(ctx context.Context, _ *queries.Queries) error {
				role, org, err = pinned(ctx, scopedConn{tt.scope})
				return err
			})
			if err != nil || role != tt.wantRole || org != tt.wantOrg {
				t.Errorf("a transaction runs as %q in %q (%v), want %q in %q", role, org, err, tt.wantRole, tt.wantOrg)
			}
		})
	}
	for range 2 * int(db.pool.Config().MaxConns) {
		if role, org, err := pinned(ctx, db.pool); err != nil || role != owner || org != "" {
			t.Fatalf("a pooled connection after pinned work is %q in %q (%v), want %q and no organization", role, org, err, owner)
		}
	}
}

// TestScopesJoin checks which work a transaction admits: its own organization's and a row's, never another's nor work
// that spans them, and for a deferred one whatever its first work pins.
func TestScopesJoin(t *testing.T) {
	t.Parallel()
	db, acme, globex, repo := scopesFixture(t)
	ctx := context.Background()
	joins := []struct {
		name         string
		outer, inner Scope
		wantErr      bool
		wantOrg      uuid.UUID
	}{
		{"an organization admits itself", db.In(globex), db.In(globex), false, globex},
		{"an organization refuses another", db.In(globex), db.In(acme), true, uuid.Nil},
		{"an organization refuses work that spans them", db.In(globex), db.Everywhere("a test"), true, uuid.Nil},
		{"an organization holds a row's work to itself", db.In(acme), db.Of(Repositories, repo), false, acme},
		{"everywhere admits an organization, unpinned", db.Everywhere("a test"), db.In(acme), false, uuid.Nil},
		{"a deferred one is pinned by its first work", db.Deferred(), db.In(acme), false, acme},
		{"a deferred one is pinned by a row's work", db.Deferred(), db.Of(Repositories, repo), false, globex},
	}
	for _, tt := range joins {
		t.Run(tt.name, func(t *testing.T) {
			var org string
			err := tt.outer.Transact(ctx, "outer", uuid.Nil, func(ctx context.Context, _ *queries.Queries) error {
				return tt.inner.Transact(ctx, "inner", uuid.Nil, func(ctx context.Context, _ *queries.Queries) (err error) {
					_, org, err = pinned(ctx, scopedConn{tt.inner})
					return err
				})
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("joining = %v, want an error %t", err, tt.wantErr)
			}
			want := ""
			if tt.wantOrg != uuid.Nil {
				want = tt.wantOrg.String()
			}
			if err == nil && org != want {
				t.Errorf("the work within runs in %q, want %q", org, want)
			}
		})
	}
}

// TestStoresReachTheDatabaseThroughScopes checks that no store keeps a way to the database around DB: outside db.go,
// only the listeners' own connections and the schema's migrations make queries or touch the pool.
func TestStoresReachTheDatabaseThroughScopes(t *testing.T) {
	t.Parallel()
	bare := regexp.MustCompile(`queries\.New\(|\.pool\b|pool\.(Query|QueryRow|Exec|Begin|BeginTx|SendBatch|Acquire)\(`)
	exempt := map[string]bool{"db.go": true, "listen.go": true, "postgres.go": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if exempt[file] || strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if bare.MatchString(line) && !strings.HasPrefix(strings.TrimSpace(line), "//") {
				t.Errorf("%s:%d reaches the database around DB's scopes: %s", file, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// BenchmarkScopedRead compares a read pinned to an organization, which sends its pin with it, with the same read
// spanning organizations, which is the query alone.
func BenchmarkScopedRead(b *testing.B) {
	pool, err := Connect(context.Background(), postgrestest.Database(b))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(pool.Close)
	schema, err := NewSchema(pool, migrations.FS, migrations.Go...)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = schema.Close() })
	if err := schema.Migrate(context.Background()); err != nil {
		b.Fatal(err)
	}
	demo, err := NewSeeds(pool, seeds.Go...)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = demo.Close() })
	if err := demo.Migrate(context.Background()); err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	db := newDB(pool)
	acme, err := db.Everywhere("a benchmark").OrganizationBySlug(ctx, "acme")
	if err != nil {
		b.Fatal(err)
	}
	for name, scope := range map[string]Scope{"pinned": db.In(acme.Organization.ID), "everywhere": db.Everywhere("a benchmark")} {
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				if _, err := scope.OrganizationByID(ctx, acme.Organization.ID); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
