// Package adminapi serves Cobranch's Admin API under /v1/, which the Back-office calls, through the server
// oapi-codegen generates from api/admin.yaml (adminapi.gen.go). Errors are problem details (RFC 9457) with a stable
// code and the request's ID, as the REST API's are.
package adminapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/server"
	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// BaseURL is where the API's paths start, as api/admin.yaml's server says.
const BaseURL = "/v1"

// Administration is what the API needs of the administration's service.
type Administration interface {
	Permissions(ctx context.Context, v auth.User) ([]string, error)
	Organizations(ctx context.Context, v auth.User, p admin.Page) (admin.List[admin.Organization], error)
	Users(ctx context.Context, v auth.User, p admin.Page) (admin.List[admin.User], error)
	CreateOrganization(ctx context.Context, v auth.User, slug, name, ownerEmail string) (admin.Organization, error)
	Enter(ctx context.Context, v auth.User, slug, reason string) (orgs.Organization, error)
	Settings(ctx context.Context, v auth.User) (admin.Settings, error)
	ChangeSettings(ctx context.Context, v auth.User, match string, change admin.SettingsChange) (admin.Settings, error)
}

// api implements api/admin.yaml's operations.
type api struct {
	// version is the version the server was built as.
	version string
	admin   Administration
}

var _ StrictServerInterface = api{}

// NewHandler serves the API under BaseURL, for a server built as version, over the administration's service. Every
// other path answers 404, and a request the API can't read 400, both as problem details. A GET's answer is the
// viewer's, and revalidated each time (Cache-Control: private, no-cache).
func NewHandler(version string, administration Administration) http.Handler {
	strict := NewStrictHandlerWithOptions(api{version: version, admin: administration}, nil, StrictHTTPServerOptions{
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
		mux.ServeHTTP(w, r)
	})
}

// GetVersion answers the version the server was built as.
func (a api) GetVersion(context.Context, GetVersionRequestObject) (GetVersionResponseObject, error) {
	return GetVersion200JSONResponse{Version: a.version}, nil
}

// GetMe answers who the request's credential signs in, with what they hold at the installation, so the Back-office
// shows what they may do.
func (a api) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	permissions, err := a.admin.Permissions(ctx, v)
	if err != nil {
		return nil, fmt.Errorf("reading the permissions: %w", err)
	}
	return GetMe200JSONResponse{Id: v.ID, DisplayName: v.DisplayName, Emails: emails(v.Emails), Permissions: nonNil(permissions)}, nil
}

// refusal is an operation's refusal: the problem the response error handler writes, with its status, stable code and
// detail. An operation returns one instead of a typed response, so every refusal takes the same path.
type refusal struct {
	status       int
	code, detail string
}

func (e *refusal) Error() string {
	return fmt.Sprintf("%d %s: %s", e.status, e.code, e.detail)
}

// refuse is a refusal.
func refuse(status int, code, detail string) error {
	return &refusal{status: status, code: code, detail: detail}
}

// viewer returns who the request signs in, or a refusal for Anonymous.
func viewer(ctx context.Context) (auth.User, error) {
	v, ok := auth.Viewer(ctx)
	if !ok {
		return auth.User{}, refuse(http.StatusUnauthorized, "auth.unauthenticated", "Sign in first.")
	}
	return v.User, nil
}

// fail maps the domain's errors to problems: kind names what wasn't found. Any other error stays one, which the
// response error handler logs and answers as internal.
func fail(err error, kind string) error {
	var invalid *orgs.ValidationError
	switch {
	case errors.As(err, &invalid):
		return refuse(http.StatusBadRequest, invalid.Code, invalid.Message)
	case errors.Is(err, auth.ErrForbidden):
		return refuse(http.StatusForbidden, "auth.forbidden", "You lack the permission this takes.")
	case errors.Is(err, orgs.ErrNotFound):
		return refuse(http.StatusNotFound, kind+".not_found", "There is no such "+kind+".")
	case errors.Is(err, orgs.ErrSlugTaken):
		return refuse(http.StatusConflict, "slug.taken", orgs.ErrSlugTaken.Error())
	}
	return err
}

// page reads a list's parameters: the search, the cursor (an ID, base64url, as next writes it) and the limit.
func page(q, cursor *string, limit *int) (admin.Page, error) {
	var p admin.Page
	if q != nil {
		p.Query = *q
	}
	if limit != nil {
		p.Limit = *limit
	}
	if cursor != nil && *cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(*cursor)
		if err == nil {
			p.After, err = uuid.FromBytes(raw)
		}
		if err != nil {
			return admin.Page{}, refuse(http.StatusBadRequest, "cursor.invalid", "The cursor isn't one a page's Link named.")
		}
	}
	return p, nil
}

// next is the Link header that names the page after p, whose last item's ID is after, relative to the request's own
// URL; nil on the last page.
func next(p admin.Page, after uuid.UUID) *string {
	if after == uuid.Nil {
		return nil
	}
	q := url.Values{"cursor": {base64.RawURLEncoding.EncodeToString(after[:])}}
	if p.Query != "" {
		q.Set("q", p.Query)
	}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	link := fmt.Sprintf(`<?%s>; rel="next"`, q.Encode())
	return &link
}

func badRequest(w http.ResponseWriter, r *http.Request, err error) {
	writeProblem(w, r, http.StatusBadRequest, "request.invalid", err.Error())
}

// internalError writes the problem an operation refused with (refusal), and hides any other err from the caller, who
// gets the request's ID to report instead.
func internalError(w http.ResponseWriter, r *http.Request, err error) {
	var refused *refusal
	if errors.As(err, &refused) {
		writeProblem(w, r, refused.status, refused.code, refused.detail)
		return
	}
	slog.ErrorContext(r.Context(), "An Admin API operation failed",
		"path", r.URL.Path, "requestId", server.RequestID(r.Context()), "error", err)
	writeProblem(w, r, http.StatusInternalServerError, "internal", "")
}

// writeProblem answers status with problem details: code is the stable error code, detail what went wrong, if
// anything more than the status says.
func writeProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	p := Problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Code: code, RequestId: server.RequestID(r.Context())}
	if detail != "" {
		p.Detail = &detail
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p) // the caller is gone; nothing to tell
}

// nonNil is s, or an empty slice for nil, so the API shows [] rather than null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func emails(es []auth.Email) []Email {
	out := make([]Email, 0, len(es))
	for _, e := range es {
		out = append(out, Email{Email: e.Email, Verified: e.Verified})
	}
	return out
}
