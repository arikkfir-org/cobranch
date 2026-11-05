package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"cobranch.dev/internal/adapters/models"
	"cobranch.dev/internal/adapters/restclient"
	"cobranch.dev/internal/adapters/server"
	"cobranch.dev/internal/system/buildinfo"
	"cobranch.dev/internal/system/config"
	"cobranch.dev/internal/system/telemetry"
)

// runModelProxy sends the harness's model requests on to the provider with the provider's key, beside the harness in
// a task's pod, until ctx ends: the key stays in its container alone, and each turn's spend is reported as the task.
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
	meter, err := restclient.New(cfg.REST.String(), cfg.TokenFile, &http.Client{Timeout: restTimeout, Transport: otelhttp.NewTransport(http.DefaultTransport)})
	if err != nil {
		return fmt.Errorf("configuring the REST client: %w", err)
	}
	handler, err := models.Proxy(models.Settings{Provider: cfg.ModelProvider, Model: cfg.Model, Key: strings.TrimSpace(string(key)), URL: url}, meter)
	if err != nil {
		return fmt.Errorf("configuring the proxy: %w", err)
	}
	ln, err := server.Listen(ctx, cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listening: %w", err)
	}
	slog.InfoContext(ctx, "Starting the model proxy", "provider", cfg.ModelProvider, "model", cfg.Model)
	if err := server.Serve(ctx, ln, handler, cfg.ShutdownTimeout); err != nil {
		return fmt.Errorf("serving: %w", err)
	}
	return nil
}
