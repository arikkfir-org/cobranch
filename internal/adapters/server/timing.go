package server

import (
	"io"
	"net/http"
	"sync"

	"github.com/felixge/httpsnoop"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"cobranch.dev/internal/system/telemetry"
)

// ServerTimingHeader names the calls a request made to Cobranch's other workloads, such as the Git server's
// internal call (telemetry.Timings).
const ServerTimingHeader = "Server-Timing"

// withTimings collects the calls each request makes to other workloads and names them in its answer's Server-Timing
// header, written once, just before the answer's status: calls still running then are named without a duration. An
// answer whose request made no call gets no header.
func withTimings(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, timings := telemetry.WithTimings(r.Context())
		var once sync.Once
		name := func() {
			once.Do(func() {
				if h := timings.Header(); h != "" {
					w.Header().Add(ServerTimingHeader, h)
				}
			})
		}
		wrapped := httpsnoop.Wrap(w, httpsnoop.Hooks{
			WriteHeader: func(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
				return func(code int) {
					// An informational status comes before the answer's own, which carries the header.
					if code >= http.StatusOK {
						name()
					}
					next(code)
				}
			},
			Write: func(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
				return func(b []byte) (int, error) { name(); return next(b) }
			},
			ReadFrom: func(next httpsnoop.ReadFromFunc) httpsnoop.ReadFromFunc {
				return func(src io.Reader) (int64, error) { name(); return next(src) }
			},
			Flush: func(next httpsnoop.FlushFunc) httpsnoop.FlushFunc {
				return func() { name(); next() }
			},
		})
		next.ServeHTTP(wrapped, r.WithContext(ctx))
	})
}

// LabelRoute names the route r was served by, the path of the pattern its ServeMux matched, on the request's
// http.server.request.duration, unless an inner ServeMux named it first. An API whose handler serves through a
// ServeMux of its own calls it once that mux has served, since the server's handler sees only its own pattern.
func LabelRoute(r *http.Request) {
	labeler, ok := otelhttp.LabelerFromContext(r.Context())
	if !ok || r.Pattern == "" {
		return
	}
	for _, kv := range labeler.Get() {
		if kv.Key == semconv.HTTPRouteKey {
			return
		}
	}
	labeler.Add(semconv.HTTPRoute(routeOf(r.Pattern)))
}

// routeOf is a ServeMux pattern's path, without its method or host: `GET /v1/orgs/{org}` is `/v1/orgs/{org}`.
func routeOf(pattern string) string {
	for i := range pattern {
		if pattern[i] == '/' {
			return pattern[i:]
		}
	}
	return pattern
}
