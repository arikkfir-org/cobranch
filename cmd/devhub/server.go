package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"

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

// api is one of DevHub's APIs, which a server of its own serves, so that the operator routes and scales each alone.
type api struct {
	// workload names the subcommand and its logs.
	workload string
	// pattern is where the server mounts the API.
	pattern string
	// handler serves the API for a server built as version.
	handler func(version string) http.Handler
}

var (
	// restServer serves the REST API, at every path but the health endpoints: an unknown one answers a problem.
	restServer = api{workload: "rest-server", pattern: "/", handler: rest.NewHandler}
	// graphqlServer serves GraphQL at /graphql.
	graphqlServer = api{workload: "graphql-server", pattern: "/graphql", handler: graphql.NewHandler}
)

// runServer serves a and the health endpoints until ctx ends. The server answers its probes from the start and turns
// ready once its stores answer and the migrate Job has brought the schema to the version this build needs.
func runServer(ctx context.Context, logs io.Writer, a api) error {
	version := buildinfo.Version()
	if err := telemetry.Setup(logs, a.workload, version); err != nil {
		return fmt.Errorf("setting up telemetry: %w", err)
	}
	cfg, err := config.LoadServer()
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
	schema, err := postgres.NewSchema(pool, migrations.FS, migrations.Go...)
	if err != nil {
		return fmt.Errorf("reading the schema's version: %w", err)
	}
	defer func() { _ = schema.Close() }() // holds nothing a server wrote

	ln, err := server.Listen(ctx, cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listening: %w", err)
	}
	handler := server.AllowOrigin(string(cfg.ConsoleOrigin), server.Handler(a.pattern, a.handler(version),
		map[string]server.Check{"schema": schema.Check, "database": postgres.Ping(pool), "bucket": blob.Check(bucket)}))
	slog.InfoContext(ctx, "Starting the server", "schemaVersion", schema.Version())
	if err := server.Serve(ctx, ln, handler, cfg.ShutdownTimeout); err != nil {
		return fmt.Errorf("serving: %w", err)
	}
	return nil
}
