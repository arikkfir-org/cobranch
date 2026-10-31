package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"cobranch.dev/internal/services/auth"
)

// Authenticator resolves a credential's token, a session's or an API key's, to who it acts as;
// auth.ErrUnauthenticated when nobody.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (auth.Authenticated, error)
}

// Authenticate resolves the request's credential and puts who it acts as on the request's context for the API to
// read (auth.Viewer): a bearer token in the Authorization header (an API key's secret), or else the session cookie,
// named cookie. A request with neither runs as Anonymous; one whose credential acts as nobody is refused with 401.
// A request with the cookie that would change something must be JSON from the Console's origin, or it is refused
// with 403: the browser sends the cookie wherever the request came from, so a cookie-authenticated mutation must come
// from the Console's own pages (consoleOrigin), and when it carries no Origin, be JSON, which no form sends. A bearer
// token is sent on purpose, so it needs no such check.
func Authenticate(cookie string, a Authenticator, consoleOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, bearer := bearerToken(r)
		if !bearer {
			c, _ := r.Cookie(cookie)
			if c == nil {
				next.ServeHTTP(w, r)
				return
			}
			if !safeMethod(r.Method) && !fromConsole(r, consoleOrigin) {
				http.Error(w, "a request with the session cookie that changes something must be JSON from the Console", http.StatusForbidden)
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
		next.ServeHTTP(w, r.WithContext(auth.WithViewer(r.Context(), viewer)))
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

// fromConsole reports whether r is JSON and, when it names its Origin, the Console's.
func fromConsole(r *http.Request, consoleOrigin string) bool {
	if ct := r.Header.Get("Content-Type"); r.ContentLength != 0 && !strings.HasPrefix(ct, "application/json") {
		return false
	}
	return SameOrigin(r, consoleOrigin)
}

// SameOrigin reports whether r came from origin: its Origin header is origin, or it carries none, which no browser's
// cross-site request lacks.
func SameOrigin(r *http.Request, origin string) bool {
	o := r.Header.Get("Origin")
	return o == "" || (origin != "" && strings.EqualFold(o, origin))
}
