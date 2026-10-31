package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/adapters/adminapi"
	"cobranch.dev/internal/adapters/blob"
	"cobranch.dev/internal/adapters/graphql"
	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/adapters/rest"
	"cobranch.dev/internal/adapters/server"
	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
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
	// pages picks the public URL of the pages that call the API, from the workloads': CORS lets them call it, and only
	// they may change something with the session cookie.
	pages func(config.URLs) config.URL
	// handler serves the API for a server built as version, over the domain's services.
	handler func(version string, s services) http.Handler
}

// services are the domain's services an API serves: sign-in's for the viewer's sessions, the organizations', the
// repositories' and the installation's administration.
type services struct {
	sessions       *auth.Service
	organizations  *orgs.Service
	repositories   *repos.Service
	administration *admin.Service
}

func console(u config.URLs) config.URL    { return u.Console }
func backOffice(u config.URLs) config.URL { return u.BackOffice }

var (
	// restServer serves the REST API, at every path but the health endpoints: an unknown one answers a problem.
	restServer = api{workload: "rest-server", pattern: "/", pages: console, handler: func(version string, s services) http.Handler {
		return rest.NewHandler(version, s.sessions, s.organizations, s.repositories)
	}}
	// graphqlServer serves GraphQL at the root path alone. Like every workload, it serves as if it were alone: the
	// operator mounts it at a host or at a path prefix that the route strips.
	graphqlServer = api{workload: "graphql-server", pattern: "/{$}", pages: console, handler: func(version string, s services) http.Handler {
		return graphql.NewHandler(version, s.sessions, s.organizations, s.repositories)
	}}
	// adminServer serves the Admin API, which the Back-office's pages call, at every path but the health endpoints.
	adminServer = api{workload: "admin-server", pattern: "/", pages: backOffice, handler: func(version string, s services) http.Handler {
		return adminapi.NewHandler(version, s.administration)
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
	// The servers only read sessions: no provider, and settings that matter at sign-in alone. The Admin server shows
	// the sign-up settings and providers the values give.
	sessions := auth.NewService(postgres.NewAuthStore(pool), nil, auth.Settings{})
	orgStore := postgres.NewOrgStore(pool)
	authorizer := auth.NewAuthorizer(orgStore)
	administration, err := adminService(cfg, orgStore, authorizer)
	if err != nil {
		return err
	}
	domain := services{sessions: sessions, organizations: orgs.NewService(orgStore, authorizer),
		repositories: repos.NewService(postgres.NewRepoStore(pool), authorizer), administration: administration}

	ln, err := server.Listen(ctx, cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listening: %w", err)
	}
	pages := a.pages(cfg.URLs).Origin()
	handler := server.AllowOrigin([]string{pages}, server.Handler(a.pattern,
		server.Authenticate(cookie.Name, sessions, pages, a.handler(version, domain)),
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
