package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/system/buildinfo"
	"cobranch.dev/internal/system/config"
	"cobranch.dev/internal/system/telemetry"
)

// runMaintenance deletes what expired, sign-in flows and sessions, once, and exits. The chart runs it as an hourly
// CronJob.
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
	service := auth.NewService(postgres.NewAuthStore(pool), nil, auth.Settings{})
	flows, sessions, err := service.DeleteExpired(ctx)
	if err != nil {
		return fmt.Errorf("maintaining the database: %w", err)
	}
	slog.InfoContext(ctx, "Deleted what expired", "signInFlows", flows, "sessions", sessions)
	return nil
}
