package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"cobranch.dev/internal/adapters/models"
	"cobranch.dev/internal/adapters/server"
	"cobranch.dev/internal/system/buildinfo"
	"cobranch.dev/internal/system/config"
	"cobranch.dev/internal/system/telemetry"
)

// runModelProxy sends the harness's model requests on to the provider with the provider's key, beside the harness in
// a task's pod, until ctx ends: the key stays in its container alone.
func runModelProxy(ctx context.Context, logs io.Writer) error {
	flush, err := telemetry.Setup(ctx, logs, "model-proxy", buildinfo.Version())
	if err != nil {
		return fmt.Errorf("setting up telemetry: %w", err)
	}
	defer flushTelemetry(ctx, flush)
	cfg, err := config.LoadModelProxy()
	if err != nil {
		return fmt.Errorf("configuring: %w", err)
	}
	key, err := os.ReadFile(cfg.ModelKeyFile)
	if err != nil {
		return fmt.Errorf("reading the provider's key: %w", err)
	}
	var url string
	if cfg.ModelURL.URL != nil {
		url = cfg.ModelURL.String()
	}
	handler, err := models.Proxy(models.Settings{Provider: cfg.ModelProvider, Key: strings.TrimSpace(string(key)), URL: url})
	if err != nil {
		return fmt.Errorf("configuring the proxy: %w", err)
	}
	ln, err := server.Listen(ctx, cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listening: %w", err)
	}
	slog.InfoContext(ctx, "Starting the model proxy", "provider", cfg.ModelProvider)
	if err := server.Serve(ctx, ln, handler, cfg.ShutdownTimeout); err != nil {
		return fmt.Errorf("serving: %w", err)
	}
	return nil
}
