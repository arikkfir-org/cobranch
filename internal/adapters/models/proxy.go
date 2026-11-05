package models

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
)

// sentHeaders are the request headers the proxy never sends on: a key the harness may send, and what names its trace or
// carries a cookie.
var sentHeaders = []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "Traceparent", "Tracestate", "Baggage", "Cookie"}

// Proxy is the model proxy of an agent's pod: it sends each turn of s.Model on to the provider s names, at its own
// address or s.URL, with s.Key in place of any key the request carries, and reports what each turn read and wrote to
// meter, which stops the task at its cap. It runs beside the harness, in a container of its own, so that the key stays
// out of the container whose tools the model drives; the harness, which holds no key, points its model at it, and
// can't reach the provider but through it, so every turn is metered.
func Proxy(s Settings, meter Meter) (http.Handler, error) {
	switch {
	case !slices.Contains(Providers(), s.Provider):
		return nil, fmt.Errorf("%q is not a model provider Cobranch knows", s.Provider)
	case s.Key == "":
		return nil, errors.New("the model proxy takes the provider's API key")
	case s.Model == "" || meter == nil:
		return nil, errors.New("the model proxy takes the task's model, and the meter it reports each turn to")
	}
	target, err := url.Parse(base(s.URL, addresses[s.Provider]))
	if err != nil {
		return nil, fmt.Errorf("reading the provider's address: %w", err)
	}
	transport := http.DefaultTransport
	if s.Client != nil && s.Client.Transport != nil {
		transport = s.Client.Transport
	}
	m := &metered{provider: s.Provider, model: s.Model, meter: meter}
	m.next = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			for _, h := range sentHeaders {
				r.Out.Header.Del(h)
			}
			// The transport then asks for a compressed answer itself, and hands the meter the answer decompressed.
			r.Out.Header.Del("Accept-Encoding")
			query := r.Out.URL.Query()
			query.Del("key")
			r.Out.URL.RawQuery = query.Encode()
			for k, v := range keyHeader(s.Provider, s.Key) {
				r.Out.Header.Set(k, v)
			}
		},
		Transport:      transport,
		ModifyResponse: m.meterAnswer,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.ErrorContext(r.Context(), "Reaching the model provider failed", "provider", s.Provider, "error", err)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	return m, nil
}
