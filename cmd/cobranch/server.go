package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/adapters/blob"
	"cobranch.dev/internal/adapters/graphql"
	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/adapters/rest"
	"cobranch.dev/internal/adapters/server"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/system/buildinfo"
	"cobranch.dev/internal/system/config"
	"cobranch.dev/internal/system/telemetry"
)

// api is one of Cobranch's APIs, which a server of its own serves, so that the operator routes and scales each alone.
type api struct {
	// workload names the subcommand and its logs.
	workload string
	// pattern is where the server mounts the API.
	pattern string
	// handler serves the API for a server built as version, over sign-in's service for the viewer's sessions and the
	// organizations' for theirs.
	handler func(version string, sessions *auth.Service, organizations *orgs.Service) http.Handler
}

var (
	// restServer serves the REST API, at every path but the health endpoints: an unknown one answers a problem.
	restServer = api{workload: "rest-server", pattern: "/", handler: func(version string, s *auth.Service, o *orgs.Service) http.Handler {
		return rest.NewHandler(version, s, o)
	}}
	// graphqlServer serves GraphQL at the root path alone. Like every workload, it serves as if it were alone: the
	// operator mounts it at a host or at a path prefix that the route strips.
	graphqlServer = api{workload: "graphql-server", pattern: "/{$}", handler: func(version string, s *auth.Service, o *orgs.Service) http.Handler {
		return graphql.NewHandler(version, s, o)
	}}
)

// runServer serves a and the health endpoints until ctx ends. The server answers its probes from the start and turns
// ready once its stores answer and the migrate Job has brought the schema to the version this build needs. Every
// request's session cookie is resolved first, so the API knows its viewer.
func runServer(ctx context.Context, logs io.Writer, a api) error {
	version := buildinfo.Version()
	flush, err := telemetry.Setup(ctx, logs, a.workload, version)
	if err != nil {
		return fmt.Errorf("setting up telemetry: %w", err)
	}
	defer flushTelemetry(ctx, flush)
	cfg, err := config.LoadServer()
	if err != nil {
		return fmt.Errorf("configuring: %w", err)
	}
	cookie, err := sessionCookie(cfg.URLs)
	if err != nil {
		return err
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
	// The servers only read sessions: no provider, and settings that matter at sign-in alone.
	sessions := auth.NewService(postgres.NewAuthStore(pool), nil, auth.Settings{})
	orgStore := postgres.NewOrgStore(pool)
	organizations := orgs.NewService(orgStore, auth.NewAuthorizer(orgStore))

	ln, err := server.Listen(ctx, cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listening: %w", err)
	}
	console := cfg.Console.Origin()
	handler := server.AllowOrigin(console, server.Handler(a.pattern,
		server.Authenticate(cookie.Name, sessions, console, a.handler(version, sessions, organizations)),
		map[string]server.Check{"schema": schema.Check, "database": postgres.Ping(pool), "bucket": blob.Check(bucket)}))
	slog.InfoContext(ctx, "Starting the server", "schemaVersion", schema.Version(), "sessionCookie", cookie.Name)
	if err := server.Serve(ctx, ln, handler, cfg.ShutdownTimeout); err != nil {
		return fmt.Errorf("serving: %w", err)
	}
	return nil
}

// sessionCookie names the session cookie from the workloads' public URLs: host-only when they share one host (or
// none is set, as in development), otherwise for their nearest common parent domain.
func sessionCookie(urls config.URLs) (auth.Cookie, error) {
	all := urls.All()
	if len(all) == 0 {
		return auth.Cookie{Name: auth.HostCookie}, nil
	}
	cookie, err := auth.CookieScope(all...)
	if err != nil {
		return auth.Cookie{}, fmt.Errorf("naming the session cookie from the workloads' URLs: %w", err)
	}
	return cookie, nil
}
