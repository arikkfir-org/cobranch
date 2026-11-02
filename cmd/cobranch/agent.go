package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"cobranch.dev/internal/adapters/models"
	"cobranch.dev/internal/adapters/restclient"
	"cobranch.dev/internal/adapters/workspace"
	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/harness"
	"cobranch.dev/internal/services/reviewer"
	"cobranch.dev/internal/system/buildinfo"
	"cobranch.dev/internal/system/config"
	"cobranch.dev/internal/system/telemetry"
)

// restTimeout bounds a call of the REST API; a model's turn may take minutes, so a provider's calls end only with ctx.
const restTimeout = time.Minute

// runAgent works the task its token names until the task ends: the harness in the task's pod, which the controller
// starts, fitted to the AI reviewer's review tasks. It exits cleanly once the task ends or Cobranch refuses its token, and fails on what a restart may get past,
// for the controller to start the pod again.
func runAgent(ctx context.Context, logs io.Writer) error {
	flush, err := telemetry.Setup(ctx, logs, "agent", buildinfo.Version())
	if err != nil {
		return fmt.Errorf("setting up telemetry: %w", err)
	}
	defer flushTelemetry(ctx, flush)
	cfg, err := config.LoadAgent()
	if err != nil {
		return fmt.Errorf("configuring: %w", err)
	}
	model, err := agentModel(cfg)
	if err != nil {
		return err
	}
	api, err := restclient.New(cfg.REST.String(), cfg.TokenFile, &http.Client{Timeout: restTimeout, Transport: otelhttp.NewTransport(http.DefaultTransport)})
	if err != nil {
		return fmt.Errorf("configuring: %w", err)
	}
	ws, err := workspace.New(cfg.Workspace, cfg.ShellTimeout)
	if err != nil {
		return fmt.Errorf("preparing the workspace: %w", err)
	}
	defer func() { _ = ws.Close() }() // only reads the directory's handle
	h := harness.New(api, model, cfg.MaxTurns, ws.Tools()...).Handle(agents.KindReview, reviewer.New(api))
	if err := h.Run(ctx); err != nil {
		return fmt.Errorf("working the task: %w", err)
	}
	return nil
}

// agentModel is the agent's model, with its provider's key from its file.
func agentModel(cfg config.Agent) (harness.Model, error) {
	key, err := os.ReadFile(cfg.ModelKeyFile)
	if err != nil {
		return nil, fmt.Errorf("reading the provider's key: %w", err)
	}
	var url string
	if cfg.ModelURL.URL != nil {
		url = cfg.ModelURL.String()
	}
	model, err := models.New(models.Settings{Provider: cfg.ModelProvider, Model: cfg.Model, Key: strings.TrimSpace(string(key)), URL: url,
		MaxTokens: cfg.ModelMaxTokens})
	if err != nil {
		return nil, fmt.Errorf("configuring the model: %w", err)
	}
	return model, nil
}
