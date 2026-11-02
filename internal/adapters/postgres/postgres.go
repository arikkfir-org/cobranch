// Package postgres connects Cobranch to PostgreSQL, the store of every fact it shows, and migrates its schema.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("cobranch.dev/internal/adapters/postgres")

// undefinedTable is PostgreSQL's error code for a table that does not exist.
const undefinedTable = "42P01"

// SeedTable records the seeds a database has, apart from the schema's migrations in goose's own table, so an
// installation that never seeds keeps every one of its schema's versions.
const SeedTable = "cobranch_seed_version"

// Connect returns a pool of connections to the database at url, or, when url is empty, to the one libpq's variables
// name (PGHOST, PGPORT, PGUSER, PGPASSWORD, PGDATABASE, PGSSLMODE). It connects lazily, so a database that is not up
// yet fails the first query, not Connect.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		// pgx's error quotes the connection string, password and all; say only where it went wrong.
		return nil, errors.New("parsing the database's connection settings: they are not a valid URL or keyword list")
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating the database's connection pool: %w", err)
	}
	return pool, nil
}

// Ping returns a readiness check that the database answers.
func Ping(pool *pgxpool.Pool) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := pool.Ping(ctx); err != nil {
			return fmt.Errorf("pinging the database: %w", err)
		}
		return nil
	}
}

// Schema is the database's schema, which this build's migrations bring to its version: the highest they number. Seeds
// are a Schema too, of demo data rather than tables, with a version table of their own.
type Schema struct {
	pool     *pgxpool.Pool
	provider *goose.Provider
	// table records the migrations the database has.
	table string
	// versions are this build's migrations, in order: the last is the schema's version.
	versions []int64
	// current is true once the database had every one of versions; it never goes back.
	current atomic.Bool
}

// NewSchema returns the schema of the database pool connects to, as migrations bring it up to date: numbered SQL files
// at the root of sqlFiles, and goMigrations for what SQL can't say, numbered among them. Close releases it.
func NewSchema(pool *pgxpool.Pool, sqlFiles fs.FS, goMigrations ...*goose.Migration) (*Schema, error) {
	return newMigrations(pool, goose.DefaultTablename, sqlFiles, goMigrations)
}

// NewSeeds returns the demo data that seeds, Go migrations numbered on their own, bring the database pool connects to,
// recorded in SeedTable. The migrate Job applies them after the schema's migrations, and only when asked to. Close
// releases it.
func NewSeeds(pool *pgxpool.Pool, seeds ...*goose.Migration) (*Schema, error) {
	return newMigrations(pool, SeedTable, nil, seeds)
}

// newMigrations returns the migrations of sqlFiles and goMigrations on the database pool connects to, recorded in table.
// It applies those the database lacks below the highest it has too, since pull requests built side by side merge in
// any order: a database that has 3 when 2 merges gets 2, where goose would otherwise refuse to migrate.
func newMigrations(pool *pgxpool.Pool, table string, sqlFiles fs.FS, goMigrations []*goose.Migration) (*Schema, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("creating the migrations' lock: %w", err)
	}
	db := stdlib.OpenDBFromPool(pool) // closing it leaves the pool open
	provider, err := goose.NewProvider(goose.DialectPostgres, db, sqlFiles,
		goose.WithSessionLocker(locker),
		goose.WithTableName(table),
		goose.WithGoMigrations(goMigrations...),
		goose.WithDisableGlobalRegistry(true),
		goose.WithAllowOutofOrder(true),
	)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("loading the migrations: %w", err), db.Close())
	}
	sources := provider.ListSources() // never empty: NewProvider refuses no migrations
	versions := make([]int64, 0, len(sources))
	for _, src := range sources {
		versions = append(versions, src.Version)
	}
	return &Schema{pool: pool, provider: provider, table: table, versions: versions}, nil
}

// Close releases the schema's handle on the pool, which stays open.
func (s *Schema) Close() error {
	if err := s.provider.Close(); err != nil {
		return fmt.Errorf("closing the migrations' database handle: %w", err)
	}
	return nil
}

// Version is the schema's version this build needs: the highest of its migrations.
func (s *Schema) Version() int64 {
	return s.versions[len(s.versions)-1]
}

// Migrate applies every migration the database lacks, under a PostgreSQL advisory lock: of several migrating at once,
// one migrates and the others wait for it, then find nothing left to do. Each migration runs in a transaction of its
// own.
func (s *Schema) Migrate(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "postgres.Schema.Migrate")
	defer span.End()
	results, err := s.provider.Up(ctx)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("migrating the database: %w", err)
	}
	for _, r := range results {
		slog.InfoContext(ctx, "Applied a migration", "table", s.table,
			"schemaVersion", r.Source.Version, "migration", r.Source.Path, "duration", r.Duration.String())
	}
	s.current.Store(true)
	return nil
}

// MigrateUntilDone calls Migrate until it succeeds, fails for good, or ctx ends, waiting between attempts from first up
// to a minute, so a migration that starts before its database waits for it rather than exiting. A failure that the
// next attempt would repeat, such as a broken migration or a missing privilege, returns at once.
func (s *Schema) MigrateUntilDone(ctx context.Context, first time.Duration) error {
	wait := first
	for {
		err := s.Migrate(ctx)
		if err == nil || !notReady(err) {
			return err
		}
		slog.WarnContext(ctx, "Could not migrate the database; trying again", "error", err, "in", wait)
		select {
		case <-ctx.Done():
			return fmt.Errorf("migrating the database: %w", context.Cause(ctx))
		case <-time.After(wait):
		}
		wait = min(2*wait, time.Minute)
	}
}

// notReady reports whether err means the database isn't ready yet: it can't be reached, drops the connection, is
// starting, shutting down or out of connections.
func notReady(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// Connection exceptions, insufficient resources and operator intervention (such as cannot_connect_now).
		return strings.HasPrefix(pgErr.Code, "08") || strings.HasPrefix(pgErr.Code, "53") ||
			strings.HasPrefix(pgErr.Code, "57P")
	}
	var connectErr *pgconn.ConnectError
	var netErr net.Error
	return errors.As(err, &connectErr) || errors.As(err, &netErr) || errors.Is(err, io.ErrUnexpectedEOF)
}

// Check reports an error until the database has every migration this build has, for readiness: the servers wait for
// the migrate Job rather than migrate. Having a higher one is not enough, since a lower one can merge after it. It
// reads the migrations without the migrations' lock, so a Job that holds it doesn't hold the probes too.
func (s *Schema) Check(ctx context.Context) error {
	if s.current.Load() {
		return nil
	}
	applied, err := s.applied(ctx)
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(s.versions, func(v int64) bool { return !applied[v] }); i >= 0 {
		return fmt.Errorf("the database's schema lacks migration %d, which this build has: cobranch migrate has not run", s.versions[i])
	}
	s.current.Store(true)
	return nil
}

// applied returns the migrations the database records, none before the first.
func (s *Schema) applied(ctx context.Context) (map[int64]bool, error) {
	// A missing table fails Query where pgx prepares it first, as it does by default, or else reading the rows.
	rows, err := s.pool.Query(ctx, "SELECT version_id FROM "+s.table+" WHERE is_applied")
	if missingTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the schema's migrations: %w", err)
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if missingTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the schema's migrations: %w", err)
	}
	out := make(map[int64]bool, len(versions))
	for _, v := range versions {
		out[v] = true
	}
	return out, nil
}

// missingTable reports whether err is PostgreSQL's answer to a query of a table that doesn't exist.
func missingTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == undefinedTable
}
