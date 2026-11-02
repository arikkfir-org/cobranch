package telemetry

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
)

func TestMetricsExporter(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "nowhere", want: "none"},
		{name: "an OTLP endpoint", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318"}, want: "otlp"},
		{name: "a metrics endpoint", env: map[string]string{"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://collector:4318/v1/metrics"}, want: "otlp"},
		{name: "Prometheus", env: map[string]string{"OTEL_METRICS_EXPORTER": "prometheus"}, want: "prometheus"},
		{name: "Prometheus beside an OTLP endpoint", env: map[string]string{"OTEL_METRICS_EXPORTER": "prometheus", "OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318"}, want: "prometheus"},
		{name: "no metric exporter", env: map[string]string{"OTEL_METRICS_EXPORTER": "none", "OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318"}, want: "none"},
		{name: "the SDK off", env: map[string]string{"OTEL_METRICS_EXPORTER": "prometheus", "OTEL_SDK_DISABLED": "true"}, want: "none"},
		{name: "another exporter", env: map[string]string{"OTEL_METRICS_EXPORTER": "statsd"}, want: "statsd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"OTEL_METRICS_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_SDK_DISABLED"} {
				setEnv(t, k, tt.env[k])
			}
			if got := MetricsExporter(); got != tt.want {
				t.Errorf("MetricsExporter() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSetupRefusesUnknownMetricsExporter(t *testing.T) {
	setEnv(t, "OTEL_METRICS_EXPORTER", "statsd")
	var out bytes.Buffer
	shutdown, err := Setup(context.Background(), &out, "rest-server", "v1.2.3")
	if err == nil {
		shutdownNow(t, shutdown)
		t.Fatal("Setup() succeeded, want an error naming the exporters Cobranch knows")
	}
	if !strings.Contains(err.Error(), "statsd") {
		t.Errorf("Setup() error = %v, want it to name the exporter", err)
	}
}

func TestSetupPushesMetrics(t *testing.T) {
	var posts atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/metrics" {
			posts.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	setEnv(t, "OTEL_METRICS_EXPORTER", "")
	setEnv(t, "OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	var out bytes.Buffer
	shutdown, err := Setup(context.Background(), &out, "rest-server", "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	countOne(t)
	shutdownNow(t, shutdown) // pushes what the reader holds
	if posts.Load() == 0 {
		t.Errorf("the collector received no metrics; logs:\n%s", out.String())
	}
}

func TestSetupServesPrometheus(t *testing.T) {
	port := freePort(t)
	setEnv(t, "OTEL_METRICS_EXPORTER", "prometheus")
	setEnv(t, "OTEL_EXPORTER_PROMETHEUS_HOST", "127.0.0.1")
	setEnv(t, "OTEL_EXPORTER_PROMETHEUS_PORT", port)
	var out bytes.Buffer
	shutdown, err := Setup(context.Background(), &out, "rest-server", "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	countOne(t)
	served := otelhttp.NewHandler(http.NotFoundHandler(), "test")
	served.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://anything.example:8443/", nil))
	url := "http://" + net.JoinHostPort("127.0.0.1", port) + "/metrics"
	body := scrape(t, url)
	for _, want := range []string{`cobranch_test_calls_total{`, `http_server_request_duration_seconds_count{`} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, "anything.example") || strings.Contains(body, "8443") {
		t.Errorf("/metrics names the host a request chose:\n%s", body)
	}
	if want := `service_name="rest-server"`; !strings.Contains(body, want) {
		t.Errorf("/metrics lacks the workload's %s:\n%s", want, body)
	}
	shutdownNow(t, shutdown)
	if _, err := http.Get(url); err == nil { //nolint:noctx // a plain probe that the endpoint is gone
		t.Errorf("/metrics still answers after the shutdown")
	}
}

// countOne adds one to a counter on the global meter provider Setup installed.
func countOne(t *testing.T) {
	t.Helper()
	calls, err := otel.Meter("test").Int64Counter("cobranch.test.calls")
	if err != nil {
		t.Fatal(err)
	}
	calls.Add(context.Background(), 1)
}

// freePort is a TCP port nothing listens on, for the Prometheus endpoint.
func freePort(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

// scrape reads url as Prometheus would.
func scrape(t *testing.T, url string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }() // read in full below
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200:\n%s", url, res.StatusCode, body)
	}
	return string(body)
}
