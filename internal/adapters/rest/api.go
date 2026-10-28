// Package rest serves DevHub's REST API under /v1/, through the server oapi-codegen generates from
// api/openapi.yaml (rest.gen.go). Errors are problem details (RFC 9457) with a stable code and the request's ID.
package rest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/arikkfir-org/devhub/internal/adapters/server"
)

// BaseURL is where the API's paths start, as api/openapi.yaml's server says.
const BaseURL = "/v1"

// api implements api/openapi.yaml's operations.
type api struct {
	// version is the version the server was built as.
	version string
}

// GetVersion answers the version the server was built as.
func (a api) GetVersion(context.Context, GetVersionRequestObject) (GetVersionResponseObject, error) {
	return GetVersion200JSONResponse{Version: a.version}, nil
}

// NewHandler serves the API under BaseURL, for a server built as version. Every other path answers 404, and a request
// the API can't read 400, both as problem details.
func NewHandler(version string) http.Handler {
	strict := NewStrictHandlerWithOptions(api{version: version}, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: internalError,
	})
	mux := http.NewServeMux()
	HandlerWithOptions(strict, StdHTTPServerOptions{BaseURL: BaseURL, BaseRouter: mux, ErrorHandlerFunc: badRequest})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, http.StatusNotFound, "route.not_found", "No operation has this method and path.")
	})
	return mux
}

func badRequest(w http.ResponseWriter, r *http.Request, err error) {
	writeProblem(w, r, http.StatusBadRequest, "request.invalid", err.Error())
}

// internalError hides err from the caller, who gets the request's ID to report instead.
func internalError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "A REST operation failed",
		"path", r.URL.Path, "requestId", server.RequestID(r.Context()), "error", err)
	writeProblem(w, r, http.StatusInternalServerError, "internal", "")
}

// writeProblem answers status with problem details: code is the stable error code, detail what went wrong, if
// anything more than the status says.
func writeProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	p := Problem{
		Type:      "about:blank",
		Title:     http.StatusText(status),
		Status:    status,
		Code:      code,
		RequestId: server.RequestID(r.Context()),
	}
	if detail != "" {
		p.Detail = &detail
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p) // the caller is gone; nothing to tell
}
