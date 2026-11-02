package adminapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"cobranch.dev/internal/adapters/server"
	"cobranch.dev/internal/services/auth"
)

// TestHandlerRecordsRoutes checks that each request's duration names the Admin API's route that served it, though the
// server resolves the viewer on a copy of the request, which the server's own handler never sees.
func TestHandlerRecordsRoutes(t *testing.T) {
	tests := []struct {
		name, method, path string
		want               string // the http.route recorded
	}{
		{name: "a route", method: "GET", path: "/v1/version", want: "/v1/version"},
		{name: "a route with a parameter", method: "GET", path: "/v1/providers/google", want: "/v1/providers/{provider}"},
		{name: "a path no operation serves", method: "GET", path: "/v1/nowhere", want: "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := sdkmetric.NewManualReader()
			before := otel.GetMeterProvider()
			otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
			defer otel.SetMeterProvider(before)
			f := newFixture(t)
			// As server.Authenticate does, the API serves a copy of the request that carries its viewer.
			authenticated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				f.handler.ServeHTTP(w, r.WithContext(auth.WithViewer(r.Context(), f.ada)))
			})
			server.Handler("/", authenticated, nil).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tt.method, tt.path, nil))

			if got := recordedRoutes(t, reader); len(got) != 1 || got[0] != tt.want {
				t.Errorf("http.route = %q, want [%q]", got, tt.want)
			}
		})
	}
}

// recordedRoutes collects the http.route of each of http.server.request.duration's data points from reader.
func recordedRoutes(t *testing.T, reader sdkmetric.Reader) []string {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var routes []string
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			h, ok := m.Data.(metricdata.Histogram[float64])
			if m.Name != "http.server.request.duration" || !ok {
				continue
			}
			for _, dp := range h.DataPoints {
				route, _ := dp.Attributes.Value(semconv.HTTPRouteKey)
				routes = append(routes, route.AsString())
			}
		}
	}
	return routes
}
