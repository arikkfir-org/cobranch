// Package server serves one of DevHub's APIs on a port of its own, beside the health endpoints every server answers.
// Every API request gets a span of its trace, and every response names its request's ID.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Handler serves api at pattern, a path or a subtree of them, and answers /healthz while the process runs and /readyz
// while every one of checks passes. It continues the caller's trace, or starts one, for every request but the probes.
func Handler(pattern string, api http.Handler, checks map[string]Check) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.Handle("GET /readyz", readyz(checks))
	mux.Handle(pattern, api)
	return otelhttp.NewHandler(withRequestID(mux), "server", otelhttp.WithFilter(notProbe))
}

// notProbe reports whether r asks for something other than the server's health, which the kubelet asks every few
// seconds and no trace needs.
func notProbe(r *http.Request) bool {
	return r.URL.Path != "/healthz" && r.URL.Path != "/readyz"
}

// Listen listens for TCP connections on addr.
func Listen(ctx context.Context, addr string) (net.Listener, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", addr, err)
	}
	return ln, nil
}

// Serve serves handler on ln until ctx ends, then stops taking connections and waits up to shutdownTimeout for the
// requests in flight. Requests do not inherit ctx, so ending it does not cancel them.
func Serve(ctx context.Context, ln net.Listener, handler http.Handler, shutdownTimeout time.Duration) error {
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	slog.InfoContext(ctx, "Serving HTTP", "address", ln.Addr().String())

	select {
	case err := <-served:
		return fmt.Errorf("serving HTTP: %w", err)
	case <-ctx.Done():
	}
	slog.InfoContext(ctx, "Shutting down", "timeout", shutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutting down HTTP: %w", err)
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serving HTTP: %w", err)
	}
	return nil
}
