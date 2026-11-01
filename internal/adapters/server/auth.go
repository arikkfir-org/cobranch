package server

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"cobranch.dev/internal/services/auth"
)

// Authenticator resolves a credential's token, a session's, a personal token or an API key's, to who it acts as;
// auth.ErrUnauthenticated when nobody.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (auth.Authenticated, error)
}

// Authenticate resolves the request's credential and puts who it acts as on the request's context for the API to
// read (auth.Viewer), with its token for the calls the API makes for the viewer (auth.Token): a bearer token in the
// Authorization header (a personal token or an API key's secret), or else
// the session cookie, named cookie. A request with neither runs as Anonymous; one whose credential acts as nobody is
// refused with 401.
// A request with the cookie that would change something must be JSON from the origin of the pages that call the API,
// or it is refused with 403: the browser sends the cookie wherever the request came from, so a cookie-authenticated
// mutation must come from those pages (pagesOrigin: the Console's, or the Back-office's for the Admin API), and when it
// carries no Origin, be JSON, which no form sends. A bearer token is sent on purpose, so it needs no such check.
func Authenticate(cookie string, a Authenticator, pagesOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, bearer := bearerToken(r)
		if !bearer {
			c, _ := r.Cookie(cookie)
			if c == nil {
				next.ServeHTTP(w, r)
				return
			}
			if !safeMethod(r.Method) && !fromPages(r, pagesOrigin) {
				http.Error(w, "a request with the session cookie that changes something must be JSON from the API's own pages", http.StatusForbidden)
				return
			}
			token = c.Value
		}
		viewer, err := a.Authenticate(r.Context(), token)
		if errors.Is(err, auth.ErrUnauthenticated) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="cobranch"`)
			http.Error(w, "the credential acts as nobody: it is unknown, expired or revoked", http.StatusUnauthorized)
			return
		}
		if err != nil {
			slog.ErrorContext(r.Context(), "Checking the credential failed", "error", err)
			http.Error(w, "the credential could not be checked", http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithViewer(auth.WithToken(r.Context(), token), viewer)))
	})
}

// bearerToken returns the bearer token the request's Authorization header carries, and whether it carries one.
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(token), true
}

// safeMethod reports whether method changes nothing, so a cross-site request with it does no harm.
func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// fromPages reports whether r is JSON and, when it names its Origin, pagesOrigin.
func fromPages(r *http.Request, pagesOrigin string) bool {
	if r.ContentLength != 0 && !isJSON(r.Header.Get("Content-Type")) {
		return false
	}
	return SameOrigin(r, pagesOrigin)
}

// isJSON reports whether contentType is JSON: application/json, or a type of it such as a merge patch's
// (application/merge-patch+json), which no form sends either.
func isJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mediaType == "application/json" || strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json")
}

// SameOrigin reports whether r came from origin: its Origin header is origin, or it carries none, which no browser's
// cross-site request lacks.
func SameOrigin(r *http.Request, origin string) bool {
	o := r.Header.Get("Origin")
	return o == "" || (origin != "" && strings.EqualFold(o, origin))
}
