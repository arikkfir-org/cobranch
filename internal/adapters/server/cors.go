package server

import "net/http"

// AllowOrigin lets pages from origin call h from their own (CORS): it answers their preflight requests and names
// origin on its answers to them. Every other origin gets h's answers alone, which a browser keeps from its page. An
// empty origin returns h.
func AllowOrigin(origin string, h http.Handler) http.Handler {
	if origin == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		if r.Header.Get("Origin") != origin {
			h.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Expose-Headers", RequestIDHeader)
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+RequestIDHeader+", traceparent, tracestate")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}
