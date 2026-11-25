package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/adapters/postgres/queries"
)

// PostgreSQL's error codes for a row that a row-level security policy refuses, and one that lacks a required column.
const (
	insufficientPrivilege = "42501"
	notNullViolation      = "23502"
)

// orgTables lists the tables that carry org_id, and whether a row of each may have none.
func orgTables(t *testing.T, pool *pgxpool.Pool) map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT table_name, is_nullable = 'YES' FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name = 'org_id' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		var nullable bool
		if err := rows.Scan(&name, &nullable); err != nil {
			t.Fatal(err)
		}
		out[name] = nullable
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(out) < 40 {
		t.Fatalf("%d tables carry org_id, want the schema's forty and more", len(out))
	}
	return out
}

// TestEveryOrganizationTableHasItsPolicy checks that each table with org_id, and organizations, has row-level
// security on and policy organization for role cobranch_tenant, comparing org_id with the pinned organization: a
// migration that adds such a table without SELECT isolate_organization('<table>') fails here.
func TestEveryOrganizationTableHasItsPolicy(t *testing.T) {
	t.Parallel()
	pool := testDatabase(t)
	if err := newSchema(t, pool, migrations.FS, migrations.Go...).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	tables := orgTables(t, pool)
	tables["organizations"] = false
	for table, shared := range tables {
		column := "org_id"
		if table == "organizations" {
			column = "id"
		}
		own := "(" + column + " = ( SELECT current_org() AS current_org))"
		wantUsing := own
		if shared {
			wantUsing = "((org_id IS NULL) OR " + own + ")"
		}
		var secured bool
		var roles []string
		var using, check string
		err := pool.QueryRow(context.Background(), `SELECT c.relrowsecurity, p.roles::text[], p.qual, p.with_check
			FROM pg_class c JOIN pg_policies p ON p.schemaname = 'public' AND p.tablename = c.relname
			WHERE c.relnamespace = 'public'::regnamespace AND c.relname = $1 AND p.policyname = 'organization' AND p.cmd = 'ALL'`,
			table).Scan(&secured, &roles, &using, &check)
		if errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("table %s carries org_id and has no policy organization: its migration must end with SELECT isolate_organization('%s')", table, table)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if !secured || len(roles) != 1 || roles[0] != "cobranch_tenant" || using != wantUsing || check != own {
			t.Errorf("table %s: row-level security on = %t, for %v, USING %s WITH CHECK %s; want on, for cobranch_tenant, USING %s WITH CHECK %s",
				table, secured, roles, using, check, wantUsing, own)
		}
	}
}

// probe runs as a transaction pinned to org and reports what it reaches of table: the role it runs as, how many rows of
// other organizations it reads, and how many of other's rows it changes and deletes.
func probe(ctx context.Context, db *DB, table string, org, other uuid.UUID) (role string, read int, changed, deleted int64, err error) {
	ident := pgx.Identifier{table}.Sanitize()
	err = db.In(org).Transact(ctx, "probing "+table, uuid.Nil, func(ctx context.Context, _ *queries.Queries) error {
		tx := ctx.Value(txKey{}).(running).tx
		if err := tx.QueryRow(ctx, "SELECT current_user, count(*) FILTER (WHERE org_id <> $1) FROM "+ident, org).Scan(&role, &read); err != nil {
			return fmt.Errorf("reading: %w", err)
		}
		tag, err := tx.Exec(ctx, "UPDATE "+ident+" SET org_id = org_id WHERE org_id = $1", other)
		if err != nil {
			return fmt.Errorf("changing: %w", err)
		}
		changed = tag.RowsAffected()
		if tag, err = tx.Exec(ctx, "DELETE FROM "+ident+" WHERE org_id = $1", other); err != nil {
			return fmt.Errorf("deleting: %w", err)
		}
		deleted = tag.RowsAffected()
		return nil
	})
	return role, read, changed, deleted, err
}

// insertCode is PostgreSQL's answer to a transaction pinned to org inserting into table a row with nothing but
// organization as. PostgreSQL checks the policy before the row's own constraints, so another organization's row is
// refused by the policy, and the pinned one's only for the columns it lacks.
func insertCode(ctx context.Context, db *DB, table string, org, as uuid.UUID) string {
	ident := pgx.Identifier{table}.Sanitize()
	err := db.In(org).Transact(ctx, "inserting into "+table, uuid.Nil, func(ctx context.Context, _ *queries.Queries) error {
		_, err := ctx.Value(txKey{}).(running).tx.Exec(ctx, "INSERT INTO "+ident+" OVERRIDING SYSTEM VALUE SELECT (jsonb_populate_record(NULL::"+
			ident+", jsonb_build_object('org_id', $1::uuid))).*", as)
		return err //nolint:wrapcheck // the test reads PostgreSQL's code from it
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return fmt.Sprint(err)
}

// TestOrganizationSeesOnlyItsRows checks, table by table, that a query pinned to one organization reaches no other's
// rows whatever it asks for: it reads none, changes none, deletes none and writes none, while the same query spanning
// organizations reaches them.
func TestOrganizationSeesOnlyItsRows(t *testing.T) {
	t.Parallel()
	pool := isolatedDatabase(t)
	ctx := context.Background()
	db := newDB(pool)
	var acme, globex uuid.UUID
	if err := pool.QueryRow(ctx, "SELECT (SELECT id FROM organizations WHERE slug = 'acme'), (SELECT id FROM organizations WHERE slug = 'globex')").Scan(&acme, &globex); err != nil {
		t.Fatal(err)
	}
	held := 0
	for table := range orgTables(t, pool) {
		if countRows(t, pool, "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()+" WHERE org_id = '"+globex.String()+"'") > 0 {
			held++
		}
		role, read, changed, deleted, err := probe(ctx, db, table, acme, globex)
		if err != nil || role != "cobranch_tenant" || read != 0 || changed != 0 || deleted != 0 {
			t.Errorf("%s: pinned to Acme as %s, read %d, changed %d and deleted %d rows of other organizations (%v), want none as cobranch_tenant",
				table, role, read, changed, deleted, err)
		}
		if code := insertCode(ctx, db, table, acme, globex); code != insufficientPrivilege {
			t.Errorf("%s: pinned to Acme, inserting Globex's row = %s, want SQLSTATE %s", table, code, insufficientPrivilege)
		}
		if code := insertCode(ctx, db, table, acme, acme); code != notNullViolation {
			t.Errorf("%s: pinned to Acme, inserting its own row = %s, want SQLSTATE %s for the columns it lacks", table, code, notNullViolation)
		}
	}
	if held < 10 {
		t.Errorf("the seeds give Globex rows in %d tables, want ten and more for the reads to prove anything", held)
	}
	if seen := countRows(t, pool, "SELECT count(DISTINCT org_id) FROM repositories"); seen < 2 {
		t.Errorf("the connecting role sees %d organizations' repositories, want both", seen)
	}
}

// TestScopesReachTheirOrganization checks what each scope's own queries find of another organization's rows.
func TestScopesReachTheirOrganization(t *testing.T) {
	t.Parallel()
	pool := isolatedDatabase(t)
	ctx := context.Background()
	db := newDB(pool)
	acme, err := db.Everywhere("a test").OrganizationBySlug(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	globex, err := db.Everywhere("a test").OrganizationBySlug(ctx, "globex")
	if err != nil {
		t.Fatal(err)
	}
	var repo uuid.UUID
	if err := pool.QueryRow(ctx, "SELECT id FROM repositories WHERE org_id = $1 LIMIT 1", globex.Organization.ID).Scan(&repo); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		scope Scope
		want  bool
	}{
		{"its organization", db.In(globex.Organization.ID), true},
		{"another organization", db.In(acme.Organization.ID), false},
		{"the repository's organization", db.Of(Repositories, repo), true},
		{"a row that isn't there", db.Of(Repositories, uuid.New()), false},
		{"several rows' organization", db.OfAny(Repositories, []uuid.UUID{repo}), true},
		{"no rows", db.OfAny(Repositories, nil), false},
		{"everywhere", db.Everywhere("a test"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.scope.RepositoryByID(ctx, repo)
			if found := err == nil; found != tt.want || (err != nil && !errors.Is(err, pgx.ErrNoRows)) {
				t.Errorf("reading Globex's repository = %v, want found %t", err, tt.want)
			}
			list, err := tt.scope.Members(ctx, globex.Organization.ID) // a query of many rows
			if err != nil || (len(list) > 0) != tt.want {
				t.Errorf("listing Globex's members = %d, %v, want some %t", len(list), err, tt.want)
			}
		})
	}
}
