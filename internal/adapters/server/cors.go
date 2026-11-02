package server

import (
	"net/http"
	"slices"
)

// AllowOrigin lets pages from origins call h from their own (CORS), with the session cookie: it answers their
// preflight requests and names their origin on its answers to them, exposing the request's ID, a list's Link, a
// resource's ETag, which they name in If-Match and If-None-Match, and the calls an answer took (Server-Timing). Every
// other origin gets h's answers alone, which a browser keeps from its page. Empty origins are none; with none, it
// returns h.
func AllowOrigin(origins []string, h http.Handler) http.Handler {
	origins = slices.DeleteFunc(slices.Clone(origins), func(o string) bool { return o == "" })
	if len(origins) == 0 {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		origin := r.Header.Get("Origin")
		if !slices.Contains(origins, origin) {
			h.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Expose-Headers", RequestIDHeader+", Link, ETag, "+ServerTimingHeader)
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, If-Match, If-None-Match, "+RequestIDHeader+", traceparent, tracestate")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}
