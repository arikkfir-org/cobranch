// Package telemetry sets up Cobranch's logs and traces. Logs are JSON, one object per line, inside a Kubernetes pod,
// for whatever collects the cluster's logs, and text elsewhere, for a person. Traces follow a request across Cobranch's
// workloads with W3C Trace Context, and go over OTLP wherever the operator points OpenTelemetry's own OTEL_ variables,
// or nowhere. Either way, every log record written inside a request names its trace and span.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/kelseyhightower/envconfig"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/system/config"
)

// Config is read from the environment by Setup, under the configuration's prefix whatever the workload:
// COBRANCH_LOG_LEVEL and COBRANCH_LOG_FORMAT. Traces take OpenTelemetry's standard variables instead (Exporting).
type Config struct {
	// LogLevel is the lowest level logged: debug, info, warn or error.
	LogLevel slog.Level `envconfig:"LOG_LEVEL" default:"info"`
	// LogFormat is json or text. Empty picks json inside a Kubernetes pod and text elsewhere.
	LogFormat string `envconfig:"LOG_FORMAT"`
}

// Setup reads the Config from the environment, installs the process-wide logger, writing to out, and the process-wide
// tracer provider and propagator. Every record carries the workload's name and the version it was built as, and so
// does every span, as its service. It returns a function that exports the spans still buffered, for the workload to
// call before it exits.
func Setup(ctx context.Context, out io.Writer, workload, version string) (func(context.Context) error, error) {
	var c Config
	if err := envconfig.Process(config.Prefix, &c); err != nil {
		return nil, fmt.Errorf("reading the telemetry's configuration from the COBRANCH_ variables: %w", err)
	}
	handler, err := logHandler(out, c)
	if err != nil {
		return nil, err
	}
	slog.SetDefault(slog.New(withTrace{handler}).With("workload", workload, "version", version))

	tp, err := tracerProvider(ctx, workload, version)
	if err != nil {
		return nil, err
	}
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.WarnContext(context.Background(), "OpenTelemetry failed", "error", err)
	}))
	return tp.Shutdown, nil
}

// logHandler writes c's format at c's level to out.
func logHandler(out io.Writer, c Config) (slog.Handler, error) {
	opts := &slog.HandlerOptions{Level: c.LogLevel}
	format := c.LogFormat
	if format == "" {
		format = "text"
		if InCluster() {
			format = "json"
		}
	}
	switch format {
	case "json":
		return slog.NewJSONHandler(out, opts), nil
	case "text":
		return slog.NewTextHandler(out, opts), nil
	default:
		return nil, fmt.Errorf("COBRANCH_LOG_FORMAT is %q: it must be json, text or empty", c.LogFormat)
	}
}

// InCluster reports whether the process runs in a Kubernetes pod, which the kubelet tells every container.
func InCluster() bool {
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

// tracerProvider makes spans for workload at version. It exports them over OTLP when Exporting, and otherwise samples
// none: requests still get trace IDs, which name them in logs and pass on to the workloads they call.
func tracerProvider(ctx context.Context, workload, version string) (*sdktrace.TracerProvider, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceNamespace("cobranch"), semconv.ServiceName(workload),
			semconv.ServiceVersion(version)),
		resource.WithFromEnv(), // OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES win
		resource.WithTelemetrySDK())
	if err != nil && !errors.Is(err, resource.ErrPartialResource) {
		return nil, fmt.Errorf("describing the workload to OpenTelemetry: %w", err)
	}
	if !Exporting() {
		return sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithSampler(sdktrace.NeverSample())), nil
	}
	exporter, err := otlptracehttp.New(ctx) // reads OTEL_EXPORTER_OTLP_*: endpoint, headers, certificate, timeout
	if err != nil {
		return nil, fmt.Errorf("creating the OTLP trace exporter: %w", err)
	}
	// OTEL_TRACES_SAMPLER and its argument choose the sampler; by default, every trace a caller didn't decline.
	return sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithBatcher(exporter)), nil
}

// Exporting reports whether the operator pointed traces somewhere: an OTLP endpoint, and neither OpenTelemetry's SDK
// nor its trace exporter turned off.
func Exporting() bool {
	if os.Getenv("OTEL_SDK_DISABLED") == "true" || os.Getenv("OTEL_TRACES_EXPORTER") == "none" {
		return false
	}
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != ""
}

// withTrace names the trace and span of a record's context, when it has them, on the record.
type withTrace struct{ slog.Handler }

func (h withTrace) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("traceId", sc.TraceID().String()), slog.String("spanId", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r) //nolint:wrapcheck // a handler's error is the logger's to report
}

func (h withTrace) WithAttrs(attrs []slog.Attr) slog.Handler {
	return withTrace{h.Handler.WithAttrs(attrs)}
}

func (h withTrace) WithGroup(name string) slog.Handler { return withTrace{h.Handler.WithGroup(name)} }
