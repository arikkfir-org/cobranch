package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/arikkfir-org/devhub/db/migrations"
	"github.com/arikkfir-org/devhub/internal/adapters/postgres"
	"github.com/arikkfir-org/devhub/internal/system/buildinfo"
	"github.com/arikkfir-org/devhub/internal/system/config"
	"github.com/arikkfir-org/devhub/internal/system/telemetry"
)

// firstMigrationRetry is how long the migrate Job waits before it tries migrating a database it could not reach again.
const firstMigrationRetry = time.Second

// runMigrate brings the database's schema to the version this build needs and exits, waiting for a database that
// doesn't answer yet until ctx ends. The chart runs it as a Job before the servers roll out.
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
	return nil
}
