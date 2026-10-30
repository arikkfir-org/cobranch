// Package postgres connects DevHub to PostgreSQL, the store of every fact it shows, and migrates its schema.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("github.com/arikkfir-org/devhub/internal/adapters/postgres")

// undefinedTable is PostgreSQL's error code for a table that does not exist.
const undefinedTable = "42P01"

// SeedTable records the seeds a database has, apart from the schema's migrations in goose's own table, so an
// installation that never seeds keeps every one of its schema's versions.
const SeedTable = "devhub_seed_version"

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
	// version is the schema's version this build needs.
	version int64
	// current is true once the database's schema reached version; it never goes back.
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
	)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("loading the migrations: %w", err), db.Close())
	}
	sources := provider.ListSources() // never empty: NewProvider refuses no migrations
	return &Schema{pool: pool, provider: provider, table: table, version: sources[len(sources)-1].Version}, nil
}

// Close releases the schema's handle on the pool, which stays open.
func (s *Schema) Close() error {
	if err := s.provider.Close(); err != nil {
		return fmt.Errorf("closing the migrations' database handle: %w", err)
	}
	return nil
}

// Version is the schema's version this build needs.
func (s *Schema) Version() int64 {
	return s.version
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

// Check reports an error until the database's schema is at least the version this build needs, for readiness: the
// servers wait for the migrate Job rather than migrate. It reads the version without the migrations' lock, so a Job
// that holds it doesn't hold the probes too.
func (s *Schema) Check(ctx context.Context) error {
	if s.current.Load() {
		return nil
	}
	current, err := s.currentVersion(ctx)
	if err != nil {
		return err
	}
	if current < s.version {
		return fmt.Errorf("the database's schema is at version %d, and this build needs %d: devhub migrate has not run",
			current, s.version)
	}
	s.current.Store(true)
	return nil
}

// currentVersion returns the highest migration the database records, or 0 before the first.
func (s *Schema) currentVersion(ctx context.Context) (int64, error) {
	var version sql.NullInt64
	err := s.pool.QueryRow(ctx, "SELECT max(version_id) FROM "+s.table).Scan(&version)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == undefinedTable {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading the schema's version: %w", err)
	}
	return version.Int64, nil
}
