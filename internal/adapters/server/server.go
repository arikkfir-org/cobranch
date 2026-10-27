// Package server serves the API server's HTTP routes on one port: its health, the REST API and GraphQL. Every response
// names its request's ID.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Routes are the API server's handlers.
type Routes struct {
	// REST serves every path under /api/.
	REST http.Handler
	// GraphQL serves /graphql.
	GraphQL http.Handler
}

// Handler routes requests to routes, and answers /healthz while the process runs and /readyz while every one of checks
// passes.
func Handler(routes Routes, checks map[string]Check) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.Handle("GET /readyz", readyz(checks))
	mux.Handle("/api/", routes.REST)
	mux.Handle("/graphql", routes.GraphQL)
	return withRequestID(mux)
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
