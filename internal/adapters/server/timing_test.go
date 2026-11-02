package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"cobranch.dev/internal/system/telemetry"
)

// serverTimingCase is an API's answer, and the Server-Timing header it should carry.
type serverTimingCase struct {
	name   string
	api    http.HandlerFunc
	want   string // a pattern of the header; empty: no header
	status int
}

func serverTimingCases() []serverTimingCase {
	const dur = `git;dur=\d+(\.\d+)?`
	return []serverTimingCase{
		{
			name: "no calls",
			api:  func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") },
		},
		{
			name: "two calls before the body",
			api: func(w http.ResponseWriter, r *http.Request) {
				telemetry.Time(r.Context(), "git")()
				telemetry.Time(r.Context(), "git")()
				_, _ = io.WriteString(w, "ok")
			},
			want: `^` + dur + `, ` + dur + `$`,
		},
		{
			name: "a call before a status alone",
			api: func(w http.ResponseWriter, r *http.Request) {
				telemetry.Time(r.Context(), "git")()
				w.WriteHeader(http.StatusNoContent)
			},
			want:   `^` + dur + `$`,
			status: http.StatusNoContent,
		},
		{
			name: "a call still running when the answer starts",
			api: func(w http.ResponseWriter, r *http.Request) {
				end := telemetry.Time(r.Context(), "git")
				_, _ = io.WriteString(w, "ok")
				end()
			},
			want: `^git$`,
		},
		{
			name: "a call after the answer started",
			api: func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, "ok")
				telemetry.Time(r.Context(), "git")()
			},
		},
	}
}

func TestServerTiming(t *testing.T) {
	for _, tt := range serverTimingCases() {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			Handler("/", tt.api, nil).ServeHTTP(rec, httptest.NewRequest("GET", "/v1/version", nil))
			if want := max(tt.status, http.StatusOK); rec.Code != want {
				t.Errorf("status = %d, want %d", rec.Code, want)
			}
			got := rec.Header().Values(ServerTimingHeader)
			switch {
			case tt.want == "" && len(got) != 0:
				t.Errorf("%s = %q, want none", ServerTimingHeader, got)
			case tt.want != "" && (len(got) != 1 || !regexp.MustCompile(tt.want).MatchString(got[0])):
				t.Errorf("%s = %q, want one matching %s", ServerTimingHeader, got, tt.want)
			}
		})
	}
}

func TestHandlerRecordsRoutes(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		api     http.Handler
		path    string
		want    string // the http.route recorded; empty: none
	}{
		{name: "the server's own pattern", pattern: "/", api: echo("api"), path: "/v1/version", want: "/"},
		{name: "the API's route", pattern: "/", api: routes(), path: "/v1/orgs/acme", want: "/v1/orgs/{org}"},
		{name: "a path no route of the API serves", pattern: "/", api: routes(), path: "/v1/nowhere", want: "/"},
		{name: "a path the server doesn't serve", pattern: "/{$}", api: echo("api"), path: "/v1/nowhere"},
		{name: "a probe", pattern: "/", api: echo("api"), path: "/healthz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := sdkmetric.NewManualReader()
			before := otel.GetMeterProvider()
			otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
			defer otel.SetMeterProvider(before)
			rec := httptest.NewRecorder()
			Handler(tt.pattern, tt.api, nil).ServeHTTP(rec, httptest.NewRequest("GET", tt.path, nil))

			routes, recorded := recordedRoutes(t, reader)
			if tt.path == "/healthz" {
				if recorded != 0 {
					t.Errorf("recorded %d durations for a probe, want none", recorded)
				}
				return
			}
			if recorded != 1 {
				t.Fatalf("recorded %d durations, want one", recorded)
			}
			if got := routes[0]; got != tt.want {
				t.Errorf("http.route = %q, want %q", got, tt.want)
			}
		})
	}
}

// routes is an API that serves its routes through a ServeMux of its own, behind a handler that copies the request,
// as authentication does, and names its route once served.
func routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /v1/orgs/{org}", echo("org"))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithoutCancel(r.Context()))
		mux.ServeHTTP(w, r)
		LabelRoute(r)
	})
}

// recordedRoutes collects http.server.request.duration from reader: the http.route of each data point, empty where
// it has none, and how many requests the points count.
func recordedRoutes(t *testing.T, reader sdkmetric.Reader) ([]string, uint64) {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var routes []string
	var count uint64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "http.server.request.duration" {
				continue
			}
			h, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s is %T, want a histogram", m.Name, m.Data)
			}
			for _, dp := range h.DataPoints {
				route, _ := dp.Attributes.Value(semconv.HTTPRouteKey)
				routes = append(routes, route.AsString())
				count += dp.Count
			}
		}
	}
	return routes, count
}
