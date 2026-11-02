// Package rest serves Cobranch's REST API under /v1/, through the server oapi-codegen generates from
// api/openapi.yaml (rest.gen.go). Errors are problem details (RFC 9457) with a stable code and the request's ID.
package rest

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"cobranch.dev/internal/adapters/server"
)

// BaseURL is where the API's paths start, as api/openapi.yaml's server says.
const BaseURL = "/v1"

// api implements api/openapi.yaml's operations.
type api struct {
	// version is the version the server was built as.
	version string
	// sessions is sign-in's service, for the viewer's sessions.
	sessions Sessions
	// orgs is the organizations' service.
	orgs Organizations
	// repos is the repositories' service.
	repos Repositories
	// code is the service of the repositories' code.
	code Code
	// pages records the Console's page views.
	pages pageMetrics
}

// GetVersion answers the version the server was built as.
func (a api) GetVersion(context.Context, GetVersionRequestObject) (GetVersionResponseObject, error) {
	return GetVersion200JSONResponse{Version: a.version}, nil
}

// NewHandler serves the API under BaseURL, for a server built as version, with sessions for the viewer's,
// organizations and repositories for theirs, and code for the repositories' branches, commits and files. Every other
// path answers 404, and a request the API can't read 400, both as problem details. A GET's answer is the viewer's, and
// revalidated each time (Cache-Control: private, no-cache). Each request's duration is recorded by its route.
func NewHandler(version string, sessions Sessions, organizations Organizations, repositories Repositories, code Code, opts ...Option) http.Handler {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	a := api{version: version, sessions: sessions, orgs: organizations, repos: repositories, code: code, pages: newPageMetrics(o.meter)}
	strict := NewStrictHandlerWithOptions(a, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: internalError,
	})
	mux := http.NewServeMux()
	HandlerWithOptions(strict, StdHTTPServerOptions{BaseURL: BaseURL, BaseRouter: mux, ErrorHandlerFunc: badRequest})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, http.StatusNotFound, "route.not_found", "No operation has this method and path.")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			w.Header().Set("Cache-Control", "private, no-cache")
		}
		if r.URL.Path == BaseURL+"/page-views" {
			r.Body = http.MaxBytesReader(w, r.Body, maxPageViewsBody) // anyone may report, within bounds
		}
		mux.ServeHTTP(w, r)
		server.LabelRoute(r)
	})
}

func badRequest(w http.ResponseWriter, r *http.Request, err error) {
	writeProblem(w, r, http.StatusBadRequest, "request.invalid", err.Error())
}

// internalError writes the problem an operation refused with (apiError), and hides any other err from the caller,
// who gets the request's ID to report instead.
func internalError(w http.ResponseWriter, r *http.Request, err error) {
	var refused *apiError
	if errors.As(err, &refused) {
		writeProblem(w, r, refused.status, refused.code, refused.detail)
		return
	}
	slog.ErrorContext(r.Context(), "A REST operation failed",
		"path", r.URL.Path, "requestId", server.RequestID(r.Context()), "error", err)
	writeProblem(w, r, http.StatusInternalServerError, "internal", "")
}

// writeProblem answers status with problem details: code is the stable error code, detail what went wrong, if
// anything more than the status says.
func writeProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	p := problem(r.Context(), status, code, detail)
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p) // the caller is gone; nothing to tell
}
