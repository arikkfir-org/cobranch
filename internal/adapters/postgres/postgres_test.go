package postgres

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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

func TestSchemaMigrate(t *testing.T) {
	tests := []struct {
		name        string
		migrations  fstest.MapFS
		servers     int
		wantVersion int64
		wantErr     bool
	}{
		{name: "DevHub's migrations", servers: 1, wantVersion: 1},
		{name: "two servers at once", servers: 2, wantVersion: 1},
		{
			name: "a table, then a column",
			migrations: fstest.MapFS{
				"00001_a.sql": {Data: []byte("-- +goose Up\nCREATE TABLE a (id int);\n")},
				"00002_b.sql": {Data: []byte("-- +goose Up\nALTER TABLE a ADD COLUMN b text;\n")},
			},
			servers:     2,
			wantVersion: 2,
		},
		{
			name:       "a broken migration",
			migrations: fstest.MapFS{"00001_a.sql": {Data: []byte("-- +goose Up\nCREATE TABLE;\n")}},
			servers:    1,
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := testDatabase(t)
			var fsys fs.FS = migrations.FS
			if tt.migrations != nil {
				fsys = tt.migrations
			}
			for i, err := range migrateAtOnce(pool, fsys, tt.servers) {
				if (err != nil) != tt.wantErr {
					t.Fatalf("server %d: Migrate() error = %v, want error %t", i, err, tt.wantErr)
				}
			}
			if tt.wantErr {
				return
			}
			var version int64
			err := pool.QueryRow(context.Background(), "SELECT max(version_id) FROM goose_db_version").Scan(&version)
			if err != nil {
				t.Fatal(err)
			}
			if version != tt.wantVersion {
				t.Errorf("schema version = %d, want %d", version, tt.wantVersion)
			}
		})
	}
}

// migrateAtOnce migrates pool's database with fsys from servers schemas at once, and returns each one's error, or the
// error its Check reports after a Migrate that succeeded.
func migrateAtOnce(pool *pgxpool.Pool, fsys fs.FS, servers int) []error {
	errs := make([]error, servers)
	var wg sync.WaitGroup
	for i := range servers {
		wg.Go(func() {
			s := NewSchema(pool, fsys)
			if errs[i] = s.Migrate(context.Background()); errs[i] == nil {
				errs[i] = s.Check(context.Background())
			}
		})
	}
	wg.Wait()
	return errs
}

func TestSchemaMigrateUntilDone(t *testing.T) {
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
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			s := NewSchema(pool, migrations.FS)
			err := s.MigrateUntilDone(ctx, 10*time.Millisecond)
			if (err != nil) != tt.wantErr {
				t.Fatalf("MigrateUntilDone() error = %v, want error %t", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("MigrateUntilDone() error = %v, want the context's", err)
			}
			if checkErr := s.Check(ctx); (checkErr != nil) != tt.wantErr {
				t.Errorf("Check() = %v after MigrateUntilDone() = %v", checkErr, err)
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
