package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/db/migrations"
	"cobranch.dev/db/seeds"
	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/system/buildinfo"
	"cobranch.dev/internal/system/config"
	"cobranch.dev/internal/system/telemetry"
)

// firstMigrationRetry is how long the migrate Job waits before it tries migrating a database it could not reach again.
const firstMigrationRetry = time.Second

// runMigrate brings the database's schema to the version this build needs, then, when asked to, adds the demo data
// (COBRANCH_SEED, the chart's seed.enabled), and exits, waiting for a database that doesn't answer yet until ctx ends.
// The chart runs it as a Job before the servers roll out.
func runMigrate(ctx context.Context, logs io.Writer) error {
	flush, err := telemetry.Setup(ctx, logs, "migrate", buildinfo.Version())
	if err != nil {
		return fmt.Errorf("setting up telemetry: %w", err)
	}
	defer flushTelemetry(ctx, flush)
	cfg, err := config.LoadMigrate()
	if err != nil {
		return fmt.Errorf("configuring: %w", err)
	}
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to the database: %w", err)
	}
	defer pool.Close()
	schema, err := postgres.NewSchema(pool, migrations.FS, migrations.Go...)
	if err != nil {
		return fmt.Errorf("reading the migrations: %w", err)
	}
	defer func() { _ = schema.Close() }() // the migrations committed, or rolled back, already

	slog.InfoContext(ctx, "Migrating the database", "schemaVersion", schema.Version())
	if err := schema.MigrateUntilDone(ctx, firstMigrationRetry); err != nil {
		return fmt.Errorf("migrating: %w", err)
	}
	slog.InfoContext(ctx, "Migrated the database", "schemaVersion", schema.Version())
	if !cfg.Seed {
		return nil
	}
	return seed(ctx, pool)
}

// seed adds the demo data the database lacks, once its schema is current: only the seeds newer than the last it has.
func seed(ctx context.Context, pool *pgxpool.Pool) error {
	demo, err := postgres.NewSeeds(pool, seeds.Go...)
	if err != nil {
		return fmt.Errorf("reading the seeds: %w", err)
	}
	defer func() { _ = demo.Close() }() // the seeds committed, or rolled back, already
	slog.InfoContext(ctx, "Seeding the database", "seedVersion", demo.Version())
	if err := demo.Migrate(ctx); err != nil {
		return fmt.Errorf("seeding: %w", err)
	}
	slog.InfoContext(ctx, "Seeded the database", "seedVersion", demo.Version())
	return nil
}
