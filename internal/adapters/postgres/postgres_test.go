package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/arikkfir-org/devhub/db/migrations"
	"github.com/arikkfir-org/devhub/internal/adapters/postgres/postgrestest"
)

// testDatabase returns a pool connected to a new, empty database, dropped when the test ends.
func testDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := Connect(context.Background(), postgrestest.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestConnect(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "URL", url: "postgres://devhub:secret@db:5432/devhub"},
		{name: "libpq's variables", url: ""},
		{name: "not a URL", url: "postgres://devhub:secret@db:port/devhub", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := Connect(context.Background(), tt.url)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Connect() error = %v, want error %t", err, tt.wantErr)
			}
			if err != nil {
				if strings.Contains(err.Error(), "secret") {
					t.Errorf("Connect() error %q shows the password", err)
				}
				return
			}
			pool.Close()
		})
	}
}

// newSchema returns the schema that sqlFiles and goMigrations bring pool's database to, closed when the test ends.
func newSchema(t *testing.T, pool *pgxpool.Pool, sqlFiles fs.FS, goMigrations ...*goose.Migration) *Schema {
	t.Helper()
	s, err := NewSchema(pool, sqlFiles, goMigrations...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

// tableA and columnB are two SQL migrations: a table, then a column of it.
var (
	tableA  = fstest.MapFS{"00001_a.sql": {Data: []byte("-- +goose Up\nCREATE TABLE a (id int);\n")}}
	columnB = fstest.MapFS{
		"00001_a.sql": tableA["00001_a.sql"],
		"00002_b.sql": {Data: []byte("-- +goose Up\nALTER TABLE a ADD COLUMN b text;\n")},
	}
)

// insertA is a Go migration, version 3, that writes a row into table a.
func insertA() *goose.Migration {
	return goose.NewGoMigration(3, &goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO a (id, b) VALUES (1, 'from Go')"); err != nil {
			return fmt.Errorf("writing a row: %w", err)
		}
		return nil
	}}, nil)
}

func TestNewSchema(t *testing.T) {
	tests := []struct {
		name         string
		sqlFiles     fs.FS
		goMigrations []*goose.Migration
		wantVersion  int64
		wantErr      bool
	}{
		{name: "DevHub's migrations", sqlFiles: migrations.FS, goMigrations: migrations.Go, wantVersion: 1},
		{name: "SQL only", sqlFiles: columnB, wantVersion: 2},
		{name: "SQL, then Go", sqlFiles: columnB, goMigrations: []*goose.Migration{insertA()}, wantVersion: 3},
		{name: "no migrations", sqlFiles: fstest.MapFS{}, wantErr: true},
		{
			name:         "Go numbered as SQL is",
			sqlFiles:     columnB,
			goMigrations: []*goose.Migration{goose.NewGoMigration(2, nil, nil)},
			wantErr:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := NewSchema(unreachableDatabase(t), tt.sqlFiles, tt.goMigrations...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewSchema() error = %v, want error %t", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			defer func() { _ = s.Close() }()
			if got := s.Version(); got != tt.wantVersion {
				t.Errorf("Version() = %d, want %d", got, tt.wantVersion)
			}
		})
	}
}

func TestSchemaMigrate(t *testing.T) {
	tests := []struct {
		name         string
		sqlFiles     fs.FS
		goMigrations []*goose.Migration
		jobs         int
		wantVersion  int64
		wantRow      bool
		wantErr      bool
	}{
		{name: "DevHub's migrations", sqlFiles: migrations.FS, goMigrations: migrations.Go, jobs: 1, wantVersion: 1},
		{name: "two Jobs at once", sqlFiles: migrations.FS, goMigrations: migrations.Go, jobs: 2, wantVersion: 1},
		{name: "a table, then a column", sqlFiles: columnB, jobs: 2, wantVersion: 2},
		{name: "SQL, then Go", sqlFiles: columnB, goMigrations: []*goose.Migration{insertA()}, jobs: 2, wantVersion: 3, wantRow: true},
		{
			name:     "a broken migration",
			sqlFiles: fstest.MapFS{"00001_a.sql": {Data: []byte("-- +goose Up\nCREATE TABLE;\n")}},
			jobs:     1,
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := testDatabase(t)
			errs := migrateAtOnce(t, pool, tt.jobs, tt.sqlFiles, tt.goMigrations...)
			if failed := slices.ContainsFunc(errs, func(err error) bool { return err != nil }); failed != tt.wantErr {
				t.Fatalf("Migrate() errors = %v, want errors %t", errs, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if version := schemaVersion(t, pool); version != tt.wantVersion {
				t.Errorf("schema version = %d, want %d", version, tt.wantVersion)
			}
			if tt.wantRow && rowsFromGo(t, pool) != 1 {
				t.Error("the Go migration's row is missing")
			}
		})
	}
}

// rowsFromGo counts the rows insertA wrote to pool's database.
func rowsFromGo(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var rows int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM a WHERE b = 'from Go'").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

// schemaVersion returns the highest migration pool's database records.
func schemaVersion(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var version int64
	if err := pool.QueryRow(context.Background(), "SELECT max(version_id) FROM goose_db_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

// migrateAtOnce migrates pool's database from jobs schemas at once, and returns each one's error, or the error its
// Check reports after a Migrate that succeeded.
func migrateAtOnce(t *testing.T, pool *pgxpool.Pool, jobs int, sqlFiles fs.FS, goMigrations ...*goose.Migration) []error {
	t.Helper()
	schemas := make([]*Schema, jobs)
	for i := range jobs {
		schemas[i] = newSchema(t, pool, sqlFiles, goMigrations...)
	}
	errs := make([]error, jobs)
	var wg sync.WaitGroup
	for i, s := range schemas {
		wg.Go(func() {
			if errs[i] = s.Migrate(context.Background()); errs[i] == nil {
				errs[i] = s.Check(context.Background())
			}
		})
	}
	wg.Wait()
	return errs
}

func TestSchemaCheck(t *testing.T) {
	tests := []struct {
		name     string
		reach    bool
		migrated fs.FS // the migrations the database has had, if any
		needs    fs.FS // the migrations of the build that checks
		wantErr  bool
	}{
		{name: "a database never migrated", reach: true, needs: tableA, wantErr: true},
		{name: "a schema behind the build", reach: true, migrated: tableA, needs: columnB, wantErr: true},
		{name: "a schema at the build's version", reach: true, migrated: columnB, needs: columnB},
		{name: "a schema ahead of the build, mid-upgrade", reach: true, migrated: columnB, needs: tableA},
		{name: "a database that never answers", needs: tableA, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := migratedDatabase(t, tt.reach, tt.migrated)
			s := newSchema(t, pool, tt.needs)
			if err := s.Check(context.Background()); (err != nil) != tt.wantErr {
				t.Errorf("Check() = %v, want error %t", err, tt.wantErr)
			}
			if tt.reach && tt.migrated == nil && schemaTableExists(t, pool) {
				t.Error("Check() created the migrations' table; it must only read")
			}
		})
	}
}

// migratedDatabase returns a database that sqlFiles have brought up to date, or, when they are nil, never migrated: one
// of its own if reach, or else one that never answers.
func migratedDatabase(t *testing.T, reach bool, sqlFiles fs.FS) *pgxpool.Pool {
	t.Helper()
	if !reach {
		return unreachableDatabase(t)
	}
	pool := testDatabase(t)
	if sqlFiles != nil {
		if err := newSchema(t, pool, sqlFiles).Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	return pool
}

// schemaTableExists reports whether pool's database has goose's table.
func schemaTableExists(t *testing.T, pool *pgxpool.Pool) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(context.Background(), "SELECT to_regclass('goose_db_version') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

func TestSchemaCheckOnceMigrated(t *testing.T) {
	pool := testDatabase(t)
	s := newSchema(t, pool, columnB)
	if err := s.Check(context.Background()); err == nil {
		t.Fatal("Check() = nil before the migrate Job ran")
	}
	if err := newSchema(t, pool, columnB).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Check(context.Background()); err != nil {
		t.Errorf("Check() = %v once the migrate Job ran", err)
	}
}

func TestSchemaMigrateUntilDone(t *testing.T) {
	broken := fstest.MapFS{"00001_a.sql": {Data: []byte("-- +goose Up\nCREATE TABLE;\n")}}
	tests := []struct {
		name         string
		reach        bool
		sqlFiles     fs.FS
		goMigrations []*goose.Migration
		wantErr      bool
		wantDeadline bool
	}{
		{name: "a database that answers", reach: true, sqlFiles: migrations.FS, goMigrations: migrations.Go},
		{name: "a database that never answers", sqlFiles: migrations.FS, goMigrations: migrations.Go, wantErr: true, wantDeadline: true},
		{name: "a broken migration", reach: true, sqlFiles: broken, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := unreachableDatabase(t)
			if tt.reach {
				pool = testDatabase(t)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			s := newSchema(t, pool, tt.sqlFiles, tt.goMigrations...)
			err := s.MigrateUntilDone(ctx, 10*time.Millisecond)
			if (err != nil) != tt.wantErr {
				t.Fatalf("MigrateUntilDone() error = %v, want error %t", err, tt.wantErr)
			}
			if gotDeadline := errors.Is(err, context.DeadlineExceeded); gotDeadline != tt.wantDeadline {
				t.Errorf("MigrateUntilDone() error = %v, want the context's %t", err, tt.wantDeadline)
			}
		})
	}
}

func TestNotReady(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "refused", err: &pgconn.ConnectError{}, want: true},
		{name: "a network timeout", err: fmt.Errorf("migrating: %w", &net.OpError{Op: "dial", Err: os.ErrDeadlineExceeded}), want: true},
		{name: "a dropped connection", err: fmt.Errorf("reading: %w", io.ErrUnexpectedEOF), want: true},
		{name: "starting up", err: &pgconn.PgError{Code: "57P03"}, want: true},
		{name: "too many connections", err: &pgconn.PgError{Code: "53300"}, want: true},
		{name: "a syntax error", err: &pgconn.PgError{Code: "42601"}},
		{name: "a missing privilege", err: &pgconn.PgError{Code: "42501"}},
		{name: "migrations out of order", err: errors.New("found 1 missing (out-of-order) migration")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := notReady(tt.err); got != tt.want {
				t.Errorf("notReady(%v) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
}

func TestPing(t *testing.T) {
	tests := []struct {
		name    string
		reach   bool
		wantErr bool
	}{
		{name: "a database that answers", reach: true},
		{name: "a database that never answers", reach: false, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := unreachableDatabase(t)
			if tt.reach {
				pool = testDatabase(t)
			}
			if err := Ping(pool)(context.Background()); (err != nil) != tt.wantErr {
				t.Errorf("Ping() = %v, want error %t", err, tt.wantErr)
			}
		})
	}
}

// unreachableDatabase returns a pool connected to a database that never answers.
func unreachableDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := Connect(context.Background(), "postgres://devhub@127.0.0.1:1/devhub?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
