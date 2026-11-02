package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/otlptranslator"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// The exporters OTEL_METRICS_EXPORTER names, as OpenTelemetry's specification does.
const (
	metricsOTLP       = "otlp"
	metricsPrometheus = "prometheus"
	metricsNone       = "none"
)

// Bucket boundaries for the histograms Cobranch records, so every backend draws them alike.
var (
	// DurationBuckets are OpenTelemetry's for HTTP durations, in seconds, from 5 ms to 10 s.
	DurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}
	// SizeBuckets are powers of four, in bytes, from 4 KiB to 1 GiB.
	SizeBuckets = []float64{1 << 12, 1 << 14, 1 << 16, 1 << 18, 1 << 20, 1 << 22, 1 << 24, 1 << 26, 1 << 28, 1 << 30}
)

// requestHosts drops the host a request named from the HTTP servers' metrics: its client chooses it, so each new one
// would add a series. The route, the method and the status say which requests they were.
var requestHosts = sdkmetric.WithView(sdkmetric.NewView(sdkmetric.Instrument{Name: "http.server.*"},
	sdkmetric.Stream{AttributeFilter: attribute.NewDenyKeysFilter(semconv.ServerAddressKey, semconv.ServerPortKey)}))

// MetricsExporter is the exporter the operator chose with OpenTelemetry's variables: OTEL_METRICS_EXPORTER, or, when
// it is unset, otlp once an OTLP endpoint is set, as traces do, and none otherwise. OTEL_SDK_DISABLED turns it off.
func MetricsExporter() string {
	if os.Getenv("OTEL_SDK_DISABLED") == "true" {
		return metricsNone
	}
	if e := os.Getenv("OTEL_METRICS_EXPORTER"); e != "" {
		return e
	}
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") != "" {
		return metricsOTLP
	}
	return metricsNone
}

// meterProvider records the workload's metrics, described by res, and exports them with MetricsExporter's choice:
// pushed over OTLP/HTTP every OTEL_METRIC_EXPORT_INTERVAL, served at /metrics in Prometheus's format on
// OTEL_EXPORTER_PROMETHEUS_HOST and OTEL_EXPORTER_PROMETHEUS_PORT, or kept in the process. It returns what stops it:
// the last push, or the endpoint's server.
func meterProvider(ctx context.Context, res *resource.Resource) (*sdkmetric.MeterProvider, func(context.Context) error, error) {
	switch e := MetricsExporter(); e {
	case metricsNone:
		mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), requestHosts)
		return mp, mp.Shutdown, nil
	case metricsOTLP:
		exporter, err := otlpmetrichttp.New(ctx) // reads OTEL_EXPORTER_OTLP_*: endpoint, headers, temporality and the rest
		if err != nil {
			return nil, nil, fmt.Errorf("creating the OTLP metric exporter: %w", err)
		}
		mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)), requestHosts)
		return mp, mp.Shutdown, nil
	case metricsPrometheus:
		return prometheusProvider(ctx, res)
	default:
		return nil, nil, fmt.Errorf("OTEL_METRICS_EXPORTER is %q: Cobranch exports metrics with otlp, prometheus or none", e)
	}
}

// prometheusProvider serves the metrics at /metrics on OTEL_EXPORTER_PROMETHEUS_HOST (localhost by default) and
// OTEL_EXPORTER_PROMETHEUS_PORT (9464 by default), each metric labelled with the workload's service.name, so a scraper
// tells the workloads apart whatever it labels its targets with.
func prometheusProvider(ctx context.Context, res *resource.Resource) (*sdkmetric.MeterProvider, func(context.Context) error, error) {
	registry := prometheus.NewRegistry()
	exporter, err := otelprometheus.New(otelprometheus.WithRegisterer(registry),
		otelprometheus.WithTranslationStrategy(otlptranslator.UnderscoreEscapingWithSuffixes),
		otelprometheus.WithResourceAsConstantLabels(attribute.NewAllowKeysFilter("service.name")))
	if err != nil {
		return nil, nil, fmt.Errorf("creating the Prometheus metric exporter: %w", err)
	}
	host, port := os.Getenv("OTEL_EXPORTER_PROMETHEUS_HOST"), os.Getenv("OTEL_EXPORTER_PROMETHEUS_PORT")
	if host == "" {
		host = "localhost"
	}
	if port == "" {
		port = "9464"
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, nil, fmt.Errorf("listening for Prometheus's scrapes on %s: %w", net.JoinHostPort(host, port), err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	logCtx := context.WithoutCancel(ctx)
	go func() {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			slog.ErrorContext(logCtx, "Serving metrics failed", "error", err)
		}
	}()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(exporter), requestHosts)
	stop := func(ctx context.Context) error {
		return errors.Join(srv.Shutdown(ctx), mp.Shutdown(ctx))
	}
	return mp, stop, nil
}
