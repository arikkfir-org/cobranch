package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/adapters/blob"
	"cobranch.dev/internal/adapters/gitserver"
	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/adapters/pushworker"
	"cobranch.dev/internal/adapters/server"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
	"cobranch.dev/internal/system/buildinfo"
	"cobranch.dev/internal/system/config"
	"cobranch.dev/internal/system/telemetry"
)

// runGitServer serves Git's smart HTTP protocol and the health endpoints until ctx ends. Its pushes' hook runs this
// binary as git-hook. It turns ready once its stores answer and the schema is at this build's version.
func runGitServer(ctx context.Context, logs io.Writer) error {
	flush, err := telemetry.Setup(ctx, logs, "git-server", buildinfo.Version())
	if err != nil {
		return fmt.Errorf("setting up telemetry: %w", err)
	}
	defer flushTelemetry(ctx, flush)
	cfg, err := config.LoadGitServer()
	if err != nil {
		return fmt.Errorf("configuring: %w", err)
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding this binary, which the hook runs: %w", err)
	}
	bucket, err := blob.Open(ctx, cfg.BlobURL)
	if err != nil {
		return fmt.Errorf("opening the bucket %s: %w", cfg.BlobURL, err)
	}
	defer func() { _ = bucket.Close() }() // every upload closed already
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to the database: %w", err)
	}
	defer pool.Close()
	schema, err := postgres.NewSchema(pool, migrations.FS, migrations.Go...)
	if err != nil {
		return fmt.Errorf("reading the schema's version: %w", err)
	}
	defer func() { _ = schema.Close() }() // holds nothing the server wrote
	orgStore := postgres.NewOrgStore(pool)
	gitServer, err := gitserver.New(ctx, gitserver.Config{DataDir: cfg.DataDir, CacheSize: int64(cfg.CacheSize), MaxPushSize: int64(cfg.MaxPushSize), Hook: []string{self, "git-hook"}},
		auth.NewService(postgres.NewAuthStore(pool), nil, auth.Settings{}),
		repos.NewService(postgres.NewRepoStore(pool), auth.NewAuthorizer(orgStore)),
		gitservice(pool), gitservice(pool), bucket)
	if err != nil {
		return fmt.Errorf("preparing the Git server: %w", err)
	}
	defer func() { _ = gitServer.Close() }() // no push runs once the server stopped
	return serve(ctx, cfg.HTTPAddr, cfg.ShutdownTimeout, schema, server.Handler("/", gitServer.Handler(),
		map[string]server.Check{"schema": schema.Check, "database": postgres.Ping(pool), "bucket": blob.Check(bucket)}))
}

// runPushWorker processes the pushes the Git server recorded, and serves the health endpoints, until ctx ends.
func runPushWorker(ctx context.Context, logs io.Writer) error {
	flush, err := telemetry.Setup(ctx, logs, "push-worker", buildinfo.Version())
	if err != nil {
		return fmt.Errorf("setting up telemetry: %w", err)
	}
	defer flushTelemetry(ctx, flush)
	cfg, err := config.LoadPushWorker()
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
		return fmt.Errorf("reading the schema's version: %w", err)
	}
	defer func() { _ = schema.Close() }() // holds nothing the worker wrote
	worked := make(chan struct{})
	go func() {
		defer close(worked)
		pushworker.Run(ctx, gitservice(pool), postgres.ListenPushes(ctx, pool), cfg.PollInterval)
	}()
	defer func() { <-worked }() // the push in hand commits or rolls back before the pool closes
	return serve(ctx, cfg.HTTPAddr, cfg.ShutdownTimeout, schema, server.Handler("/", http.NotFoundHandler(),
		map[string]server.Check{"schema": schema.Check, "database": postgres.Ping(pool)}))
}

// gitservice is Git hosting's domain over pool's database.
func gitservice(pool *pgxpool.Pool) *git.Service {
	return git.NewService(postgres.NewGitStore(pool))
}

// serve listens on addr and serves handler until ctx ends, then waits up to timeout for requests in flight.
func serve(ctx context.Context, addr string, timeout time.Duration, schema *postgres.Schema, handler http.Handler) error {
	ln, err := server.Listen(ctx, addr)
	if err != nil {
		return fmt.Errorf("listening: %w", err)
	}
	slog.InfoContext(ctx, "Starting the server", "schemaVersion", schema.Version())
	if err := server.Serve(ctx, ln, handler, timeout); err != nil {
		return fmt.Errorf("serving: %w", err)
	}
	return nil
}

// runGitHook is a push's pre-receive hook, which Git runs and which calls the Git server that started the push. Its
// exit code accepts the push or refuses it.
func runGitHook(ctx context.Context, stdin io.Reader, stderr io.Writer) int {
	cfg, err := config.LoadHook()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, git.MessageNotRecorded) // stderr is the client's; nothing else to tell
		return 1
	}
	return gitserver.RunHook(ctx, gitserver.HookEnv{URL: cfg.URL, Token: cfg.Token, Quarantine: os.Getenv("GIT_QUARANTINE_PATH"),
		ObjectDirectory: os.Getenv("GIT_OBJECT_DIRECTORY"), Alternates: os.Getenv("GIT_ALTERNATE_OBJECT_DIRECTORIES")}, stdin, stderr)
}
