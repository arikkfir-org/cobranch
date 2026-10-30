package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"cobranch.dev/internal/services/auth"
)

// Authenticator resolves a session token to who it signs in; auth.ErrUnauthenticated when nobody.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (auth.Authenticated, error)
}

// Authenticate resolves the request's session cookie, named cookie, and puts who it signs in on the request's
// context for the API to read (auth.Viewer). A request without the cookie runs as Anonymous; one whose cookie signs
// nobody in is refused with 401, as is one that would change something but isn't JSON from the Console's origin:
// the browser sends the cookie wherever the request came from, so a cookie-authenticated mutation must come from the
// Console's own pages (consoleOrigin), and when it carries no Origin, be JSON, which no form sends.
func Authenticate(cookie string, a Authenticator, consoleOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Cookie(cookie)
		if c == nil {
			next.ServeHTTP(w, r)
			return
		}
		if !safeMethod(r.Method) && !fromConsole(r, consoleOrigin) {
			http.Error(w, "a request with the session cookie that changes something must be JSON from the Console", http.StatusForbidden)
			return
		}
		viewer, err := a.Authenticate(r.Context(), c.Value)
		if errors.Is(err, auth.ErrUnauthenticated) {
			http.Error(w, "the session cookie signs nobody in", http.StatusUnauthorized)
			return
		}
		if err != nil {
			slog.ErrorContext(r.Context(), "Checking the session failed", "error", err)
			http.Error(w, "the session could not be checked", http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithViewer(r.Context(), viewer)))
	})
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
