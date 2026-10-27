package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/arikkfir-org/devhub/db/migrations"
	"github.com/arikkfir-org/devhub/internal/adapters/blob"
	"github.com/arikkfir-org/devhub/internal/adapters/graphql"
	"github.com/arikkfir-org/devhub/internal/adapters/postgres"
	"github.com/arikkfir-org/devhub/internal/adapters/rest"
	"github.com/arikkfir-org/devhub/internal/adapters/server"
	"github.com/arikkfir-org/devhub/internal/system/buildinfo"
	"github.com/arikkfir-org/devhub/internal/system/config"
	"github.com/arikkfir-org/devhub/internal/system/telemetry"
)

// firstMigrationRetry is how long the API server waits before it tries migrating a database it could not reach again.
const firstMigrationRetry = time.Second

// runAPIServer serves REST, GraphQL and the health endpoints until ctx ends, migrating the database meanwhile: the
// server answers its probes from the start and turns ready once the schema is migrated and its stores answer.
func runAPIServer(ctx context.Context, logs io.Writer) error {
	version := buildinfo.Version()
	if err := telemetry.Setup(logs, "api-server", version); err != nil {
		return fmt.Errorf("setting up telemetry: %w", err)
	}
	cfg, err := config.LoadAPIServer()
	if err != nil {
		return fmt.Errorf("configuring: %w", err)
	}
	bucket, err := blob.Open(ctx, cfg.BlobURL)
	if err != nil {
		return fmt.Errorf("opening the bucket %s: %w", cfg.BlobURL, err)
	}
	defer func() { _ = bucket.Close() }() // nothing is left to write
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to the database: %w", err)
	}
	defer pool.Close()
	schema := postgres.NewSchema(pool, migrations.FS)

	ln, err := server.Listen(ctx, cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listening: %w", err)
	}
	handler := server.Handler(
		server.Routes{
			REST:    rest.NewHandler(version),
			GraphQL: graphql.NewHandler(version),
		},
		map[string]server.Check{"schema": schema.Check, "database": postgres.Ping(pool), "bucket": blob.Check(bucket)},
	)
	slog.InfoContext(ctx, "Starting the API server")

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return server.Serve(gctx, ln, handler, cfg.ShutdownTimeout) })
	g.Go(func() error {
		err := schema.MigrateUntilDone(gctx, firstMigrationRetry)
		if err == nil || errors.Is(err, context.Canceled) {
			return nil // migrated, or stopped before the database answered: Serve reports why
		}
		return fmt.Errorf("migrating: %w", err)
	})
	if err := g.Wait(); err != nil {
		return fmt.Errorf("serving: %w", err)
	}
	return nil
}
