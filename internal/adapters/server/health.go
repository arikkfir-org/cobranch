package server

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Check reports whether a dependency can serve: nil when it can.
type Check func(context.Context) error

// checkTimeout bounds each readiness check, well inside the probe's own timeout.
const checkTimeout = 2 * time.Second

// healthz answers while the process runs: liveness asks nothing of the dependencies, so their outage restarts no pod.
func healthz(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok\n")) // the prober is gone; nothing to tell
}

// readyz answers 200 while every check passes, and otherwise 503 with the names of those that fail. Their errors go
// to the log, not to the prober.
func readyz(checks map[string]Check) http.Handler {
	names := make([]string, 0, len(checks))
	for name := range checks {
		names = append(names, name)
	}
	slices.Sort(names)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var failed []string
		for _, name := range names {
			ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
			err := checks[name](ctx)
			cancel()
			if err != nil {
				slog.WarnContext(r.Context(), "Not ready", "check", name, "error", err)
				failed = append(failed, name)
			}
		}
		if len(failed) > 0 {
			http.Error(w, "not ready: "+strings.Join(failed, ", "), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ready\n")) // the prober is gone; nothing to tell
	})
}
