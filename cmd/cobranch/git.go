package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/ssh"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/adapters/blob"
	"cobranch.dev/internal/adapters/gitinternal"
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

// runGitServer serves Git's smart HTTP protocol, SSH given an address, the internal call and the health endpoints
// until ctx ends. Its pushes' hook runs this binary as git-hook. It turns ready once its stores answer and the schema
// is at this build's version.
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
	handler := server.Handler("/", gitServer.Handler(), map[string]server.Check{"schema": schema.Check, "database": postgres.Ping(pool), "bucket": blob.Check(bucket)})
	internal := server.Handler(gitinternal.ReposPath, gitServer.InternalHandler(gitinternal.NewToken(postgres.NewInternalTokens(pool).Read)), nil)
	servers := []func(context.Context) error{
		func(ctx context.Context) error { return serve(ctx, cfg.HTTPAddr, cfg.ShutdownTimeout, schema, handler) },
		func(ctx context.Context) error {
			return serve(ctx, cfg.InternalAddr, cfg.ShutdownTimeout, schema, internal)
		},
	}
	if cfg.SSHAddr != "" {
		ssh, err := sshServer(ctx, cfg, gitServer)
		if err != nil {
			return err
		}
		servers = append(servers, ssh)
	}
	return serveTogether(ctx, servers...)
}

// sshServer listens for Git over SSH at cfg.SSHAddr, and returns what serves it until its context ends.
func sshServer(ctx context.Context, cfg config.GitServer, gitServer *gitserver.Server) (func(context.Context) error, error) {
	hostKey, err := ssh.ParsePrivateKey([]byte(cfg.SSHHostKey))
	if err != nil {
		return nil, fmt.Errorf("reading COBRANCH_GIT_SSH_HOST_KEY: %w", err)
	}
	ln, err := server.Listen(ctx, cfg.SSHAddr)
	if err != nil {
		return nil, fmt.Errorf("listening for SSH: %w", err)
	}
	return func(ctx context.Context) error {
		if err := gitServer.ServeSSH(ctx, ln, hostKey, cfg.ShutdownTimeout); err != nil {
			return fmt.Errorf("serving SSH: %w", err)
		}
		return nil
	}, nil
}

// serveTogether runs servers until ctx ends or one of them fails, which stops the others, and returns their errors.
func serveTogether(ctx context.Context, servers ...func(context.Context) error) error {
	work, stop := context.WithCancel(ctx)
	defer stop()
	served := make(chan error, len(servers))
	for _, run := range servers {
		go func() {
			defer stop()
			served <- run(work)
		}()
	}
	errs := make([]error, 0, len(servers))
	for range servers {
		errs = append(errs, <-served)
	}
	return errors.Join(errs...)
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
	work, stop := context.WithCancel(ctx)
	worked := make(chan struct{})
	go func() {
		defer close(worked)
		pushworker.Run(work, gitservice(pool), postgres.ListenPushes(work, pool), cfg.PollInterval)
	}()
	// The worker stops with the server, even one that failed before ctx ended, and the push in hand commits or rolls
	// back before the pool closes.
	defer func() { stop(); <-worked }()
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
