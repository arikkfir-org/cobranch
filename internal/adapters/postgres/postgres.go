// Package postgres connects DevHub to PostgreSQL, the store of every fact it shows, and keeps its schema migrated.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("github.com/arikkfir-org/devhub/internal/adapters/postgres")

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

// Schema migrates the database and reports whether it has.
type Schema struct {
	pool       *pgxpool.Pool
	migrations fs.FS
	migrated   atomic.Bool
}

// NewSchema returns the schema of the database pool connects to, which migrations, numbered SQL files at their root,
// bring up to date.
func NewSchema(pool *pgxpool.Pool, migrations fs.FS) *Schema {
	return &Schema{pool: pool, migrations: migrations}
}

// Migrate applies every migration the database lacks, under a PostgreSQL advisory lock: of several servers starting
// together, one migrates and the others wait for it, then find nothing left to do. Each migration runs in a
// transaction of its own.
func (s *Schema) Migrate(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "postgres.Schema.Migrate")
	defer span.End()
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("creating the migrations' lock: %w", err)
	}
	db := stdlib.OpenDBFromPool(s.pool)
	defer func() { _ = db.Close() }() // closes only this handle; the pool stays open
	provider, err := goose.NewProvider(goose.DialectPostgres, db, s.migrations, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("loading the migrations: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("migrating the database: %w", err)
	}
	for _, r := range results {
		slog.InfoContext(ctx, "Applied a migration", "migration", r.Source.Path, "duration", r.Duration.String())
	}
	s.migrated.Store(true)
	return nil
}

// MigrateUntilDone calls Migrate until it succeeds or ctx ends, waiting between attempts from first up to a minute, so
// a server that starts before its database waits for it rather than exiting.
func (s *Schema) MigrateUntilDone(ctx context.Context, first time.Duration) error {
	wait := first
	for {
		err := s.Migrate(ctx)
		if err == nil {
			return nil
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

// Check reports an error until the schema is migrated, for readiness.
func (s *Schema) Check(context.Context) error {
	if !s.migrated.Load() {
		return errors.New("the database's schema is not migrated yet")
	}
	return nil
}
