package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel"
)

func TestSetup(t *testing.T) {
	tests := []struct {
		name     string
		level    string
		wantLogs int
		wantErr  bool
	}{
		{name: "info by default", level: "", wantLogs: 1},
		{name: "debug", level: "debug", wantLogs: 2},
		{name: "error", level: "error", wantLogs: 0},
		{name: "unknown level", level: "loud", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer slog.SetDefault(slog.Default())
			setEnv(t, "COBRANCH_LOG_LEVEL", tt.level)
			setEnv(t, "COBRANCH_LOG_FORMAT", "json")
			var out bytes.Buffer
			shutdown, err := Setup(context.Background(), &out, "rest-server", "v1.2.3")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Setup() error = %v, want error %t", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			defer shutdownNow(t, shutdown)
			ctx := context.Background()
			slog.DebugContext(ctx, "Debugging")
			slog.InfoContext(ctx, "Started")
			checkRecords(t, out.Bytes(), tt.wantLogs)
		})
	}
}

func TestLogFormat(t *testing.T) {
	tests := []struct {
		name      string
		format    string
		inCluster bool
		wantJSON  bool
		wantErr   bool
	}{
		{name: "in a pod", inCluster: true, wantJSON: true},
		{name: "outside a cluster", wantJSON: false},
		{name: "json outside a cluster", format: "json", wantJSON: true},
		{name: "text in a pod", format: "text", inCluster: true, wantJSON: false},
		{name: "another format", format: "xml", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer slog.SetDefault(slog.Default())
			setEnv(t, "COBRANCH_LOG_FORMAT", tt.format)
			host := ""
			if tt.inCluster {
				host = "10.0.0.1"
			}
			setEnv(t, "KUBERNETES_SERVICE_HOST", host)
			var out bytes.Buffer
			shutdown, err := Setup(context.Background(), &out, "rest-server", "v1.2.3")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Setup() error = %v, want error %t", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			defer shutdownNow(t, shutdown)
			slog.InfoContext(context.Background(), "Started")
			if isJSON := json.Valid(bytes.TrimSpace(out.Bytes())); isJSON != tt.wantJSON {
				t.Errorf("logged %q, JSON = %t, want %t", out.String(), isJSON, tt.wantJSON)
			}
		})
	}
}

func TestLogsNameTheirTrace(t *testing.T) {
	defer slog.SetDefault(slog.Default())
	setEnv(t, "COBRANCH_LOG_FORMAT", "json")
	setEnv(t, "OTEL_EXPORTER_OTLP_ENDPOINT", "")
	var out bytes.Buffer
	shutdown, err := Setup(context.Background(), &out, "rest-server", "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownNow(t, shutdown)

	ctx, span := otel.Tracer("test").Start(context.Background(), "request")
	slog.InfoContext(ctx, "In a request")
	span.End()
	slog.InfoContext(context.Background(), "Outside one")

	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("logged %q, want two records", out.String())
	}
	var inside, outside map[string]any
	if err := json.Unmarshal(lines[0], &inside); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(lines[1], &outside); err != nil {
		t.Fatal(err)
	}
	sc := span.SpanContext()
	if !sc.IsValid() || inside["traceId"] != sc.TraceID().String() || inside["spanId"] != sc.SpanID().String() {
		t.Errorf("the record inside the request = %v, want trace %s and span %s", inside, sc.TraceID(), sc.SpanID())
	}
	if _, ok := outside["traceId"]; ok {
		t.Errorf("the record outside a request = %v, want no trace", outside)
	}
}

func TestExporting(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "nowhere", want: false},
		{name: "an OTLP endpoint", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318"}, want: true},
		{name: "a traces endpoint", env: map[string]string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://collector:4318/v1/traces"}, want: true},
		{name: "no trace exporter", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318", "OTEL_TRACES_EXPORTER": "none"}, want: false},
		{name: "the SDK off", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318", "OTEL_SDK_DISABLED": "true"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_TRACES_EXPORTER", "OTEL_SDK_DISABLED"} {
				setEnv(t, k, tt.env[k])
			}
			if got := Exporting(); got != tt.want {
				t.Errorf("Exporting() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestSetupExportsSpans(t *testing.T) {
	defer slog.SetDefault(slog.Default())
	var posts atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/traces" {
			posts.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	setEnv(t, "OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	var out bytes.Buffer
	shutdown, err := Setup(context.Background(), &out, "rest-server", "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("test").Start(context.Background(), "request")
	span.End()
	shutdownNow(t, shutdown) // exports what the batcher holds
	if posts.Load() == 0 {
		t.Errorf("the collector received no spans; logs:\n%s", out.String())
	}
	if strings.Contains(out.String(), "OpenTelemetry failed") {
		t.Errorf("exporting failed:\n%s", out.String())
	}
}

// setEnv sets the variable k to v for the test, or unsets it when v is empty.
func setEnv(t *testing.T, k, v string) {
	t.Helper()
	t.Setenv(k, v) // restored when the test ends
	if v != "" {
		return
	}
	if err := os.Unsetenv(k); err != nil {
		t.Fatal(err)
	}
}

// shutdownNow flushes and stops the tracer provider Setup installed.
func shutdownNow(t *testing.T, shutdown func(context.Context) error) {
	t.Helper()
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutting telemetry down: %v", err)
	}
}

// checkRecords checks that out holds want JSON records, each naming the workload and the version.
func checkRecords(t *testing.T, out []byte, want int) {
	t.Helper()
	var lines [][]byte
	if len(out) > 0 {
		lines = bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	}
	if len(lines) != want {
		t.Fatalf("logged %d records, want %d:\n%s", len(lines), want, out)
	}
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("record %q is not JSON: %v", line, err)
		}
		if record["workload"] != "rest-server" || record["version"] != "v1.2.3" {
			t.Errorf("record %q lacks the workload and version", line)
		}
	}
}
