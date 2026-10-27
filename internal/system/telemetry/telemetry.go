// Package telemetry sets up DevHub's logs: JSON on stdout, one object per line, for whatever collects the cluster's
// logs. OpenTelemetry's traces and metrics join them over OTLP with the first workload that needs them.
package telemetry

import (
	"fmt"
	"io"
	"log/slog"

	"github.com/kelseyhightower/envconfig"

	"github.com/arikkfir-org/devhub/internal/system/config"
)

// Config is read from the environment by Setup, under the configuration's prefix whatever the workload:
// DEVHUB_LOG_LEVEL.
type Config struct {
	// LogLevel is the lowest level logged: debug, info, warn or error.
	LogLevel slog.Level `envconfig:"LOG_LEVEL" default:"info"`
}

// Setup reads the Config from the environment and installs the process-wide logger, writing to out. Every record
// carries the workload's name and the version it was built as.
func Setup(out io.Writer, workload, version string) error {
	var c Config
	if err := envconfig.Process(config.Prefix, &c); err != nil {
		return fmt.Errorf("reading the telemetry's configuration from the DEVHUB_ variables: %w", err)
	}
	handler := slog.NewJSONHandler(out, &slog.HandlerOptions{Level: c.LogLevel})
	slog.SetDefault(slog.New(handler).With("workload", workload, "version", version))
	return nil
}
