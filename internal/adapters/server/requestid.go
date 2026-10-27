package server

import (
	"context"
	"crypto/rand"
	"net/http"
	"regexp"
)

// RequestIDHeader names a request's ID, on the request when the caller chose it and on every response.
const RequestIDHeader = "X-Request-Id"

// callerRequestID is what a caller's own request ID may look like; any other is replaced.
var callerRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

type requestIDKey struct{}

// RequestID returns the ID of the request ctx belongs to, or "" outside one.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// withRequestID gives every request an ID, the caller's when it sent a sane one, puts it in the request's context and
// names it in the response.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !callerRequestID.MatchString(id) {
			id = rand.Text()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}
