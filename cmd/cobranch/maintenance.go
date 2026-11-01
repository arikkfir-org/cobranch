package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	gocloudblob "gocloud.dev/blob"

	"cobranch.dev/internal/adapters/blob"
	"cobranch.dev/internal/adapters/gitserver"
	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/system/buildinfo"
	"cobranch.dev/internal/system/config"
	"cobranch.dev/internal/system/telemetry"
)

// runMaintenance deletes what expired, sign-in flows, sessions, invitations and join requests, and, given a number of
// packs to compact at, deletes the packs nobody lists any more and compacts the repositories that have too many, and,
// while objects move between stores, copies the old store over, once, and exits. The chart runs it as an hourly
// CronJob. A failure of one part leaves the others to run, and fails the run.
func runMaintenance(ctx context.Context, logs io.Writer) error {
	flush, err := telemetry.Setup(ctx, logs, "maintenance", buildinfo.Version())
	if err != nil {
		return fmt.Errorf("setting up telemetry: %w", err)
	}
	defer flushTelemetry(ctx, flush)
	cfg, err := config.LoadMaintenance()
	if err != nil {
		return fmt.Errorf("configuring: %w", err)
	}
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to the database: %w", err)
	}
	defer pool.Close()
	expired := deleteExpired(ctx, pool)
	if cfg.CompactAt == 0 && cfg.BlobMoveFrom == "" {
		return expired
	}
	return errors.Join(expired, maintainPacks(ctx, cfg, pool))
}

// deleteExpired deletes the sign-in flows, sessions, invitations and join requests that expired.
func deleteExpired(ctx context.Context, pool *pgxpool.Pool) error {
	flows, sessions, err := auth.NewService(postgres.NewAuthStore(pool), nil, auth.Settings{}).DeleteExpired(ctx)
	if err != nil {
		return fmt.Errorf("maintaining the database: %w", err)
	}
	orgStore := postgres.NewOrgStore(pool)
	invitations, requests, err := orgs.NewService(orgStore, auth.NewAuthorizer(orgStore)).DeleteExpired(ctx)
	if err != nil {
		return fmt.Errorf("maintaining the database: %w", err)
	}
	slog.InfoContext(ctx, "Deleted what expired", "signInFlows", flows, "sessions", sessions, "invitations", invitations, "joinRequests", requests)
	return nil
}

// maintainPacks deletes the packs nobody lists any more and compacts the repositories with cfg.CompactAt active packs or
// more, given cfg.CompactAt, then copies the store objects move from over, each past the others' failures.
func maintainPacks(ctx context.Context, cfg config.Maintenance, pool *pgxpool.Pool) error {
	stores, err := blob.OpenStores(ctx, cfg.BlobURL, cfg.BlobMoveFrom, pool)
	if err != nil {
		return fmt.Errorf("opening the object store: %w", err)
	}
	defer func() { _ = stores.Close() }() // every upload closed already
	var compactErr error
	if cfg.CompactAt != 0 {
		compactErr = compactPacks(ctx, cfg, pool, stores.Bucket)
	}
	return errors.Join(compactErr, moveObjects(ctx, cfg, stores))
}

// compactPacks deletes the packs nobody lists any more, then compacts the repositories with cfg.CompactAt active packs
// or more, each past the other's failures.
func compactPacks(ctx context.Context, cfg config.Maintenance, pool *pgxpool.Pool, bucket *gocloudblob.Bucket) error {
	packs, err := gitserver.NewPacks(cfg.GitDataDir, "", bucket)
	if err != nil {
		return fmt.Errorf("preparing to repack: %w", err)
	}
	store := postgres.NewGitStore(pool)
	service := git.NewService(store).WithPacks(store, packs)
	swept, sweepErr := service.Sweep(ctx)
	slog.InfoContext(ctx, "Deleted the packs nobody lists", "superseded", swept.Superseded, "unlisted", swept.Unlisted)
	compacted, compactErr := service.Compact(ctx, cfg.CompactAt)
	slog.InfoContext(ctx, "Compacted the repositories' packs", "repositories", compacted, "compactAt", cfg.CompactAt)
	return errors.Join(sweepErr, compactErr)
}

// moveObjects copies the store objects move from into the new one, when they move, and logs how many are left: none
// tells the operator that the old store may go.
func moveObjects(ctx context.Context, cfg config.Maintenance, stores *blob.Stores) error {
	if !stores.Moving() {
		return nil
	}
	moved, err := stores.Move(ctx)
	if moved.Left == 0 && err == nil {
		slog.InfoContext(ctx, "Moved every object: stop naming the old store", "from", blob.Name(cfg.BlobMoveFrom), "to", blob.Name(cfg.BlobURL), "copied", moved.Copied)
		return nil
	}
	slog.InfoContext(ctx, "Moved objects, some left", "from", blob.Name(cfg.BlobMoveFrom), "to", blob.Name(cfg.BlobURL), "copied", moved.Copied, "left", moved.Left)
	if err != nil {
		return fmt.Errorf("moving objects from %s: %w", blob.Name(cfg.BlobMoveFrom), err)
	}
	return nil
}
