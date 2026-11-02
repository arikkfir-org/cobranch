package models

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxy(t *testing.T) {
	tt := []struct {
		provider, wantPath string
		wantHeader         [2]string
		answer             string
	}{
		{provider: "anthropic", wantPath: "/base/v1/messages", wantHeader: [2]string{"x-api-key", "test-key"}, answer: `{"content":[{"type":"text","text":"Done."}]}`},
		{provider: "xai", wantPath: "/base/chat/completions", wantHeader: [2]string{"Authorization", "Bearer test-key"},
			answer: `{"choices":[{"message":{"role":"assistant","content":"Done."}}]}`},
		{provider: "google", wantPath: "/base/v1beta/models/test-model:generateContent", wantHeader: [2]string{"x-goog-api-key", "test-key"},
			answer: `{"candidates":[{"content":{"role":"model","parts":[{"text":"Done."}]}}]}`},
	}
	for _, tc := range tt {
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			upstream := fakeProvider(t, tc.wantPath, tc.wantHeader, tc.answer)
			defer upstream.Close()
			handler, err := Proxy(Settings{Provider: tc.provider, Key: "test-key", URL: upstream.URL + "/base/"})
			if err != nil {
				t.Fatal(err)
			}
			proxy := httptest.NewServer(handler)
			defer proxy.Close()
			m, err := New(Settings{Provider: tc.provider, Model: "test-model", URL: proxy.URL})
			if err != nil {
				t.Fatal(err)
			}
			if got, err := m.Next(context.Background(), conversation); err != nil || got.Text != "Done." {
				t.Errorf("Next() through the proxy = %+v, %v", got, err)
			}
			sendStolen(t, proxy.URL+strings.TrimPrefix(tc.wantPath, "/base"))
		})
	}
}

// fakeProvider is a provider that answers answer at wantPath, and fails the test unless a request carries the proxy's
// key in wantHeader, and no other key, trace context or cookie.
func fakeProvider(t *testing.T, wantPath string, wantHeader [2]string, answer string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys := 0
		for _, h := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
			keys += len(r.Header.Values(h))
		}
		if r.URL.Path != wantPath || r.Header.Get(wantHeader[0]) != wantHeader[1] || keys != 1 || r.URL.Query().Has("key") ||
			r.Header.Get("Traceparent") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("the provider was sent %s %s?%s with %v", r.Method, r.URL.Path, r.URL.RawQuery, r.Header)
		}
		_, _ = io.WriteString(w, answer) // the test fails on what the model reads
	}))
}

// sendStolen sends url a request with keys, trace context and a cookie of its own, which the proxy must not send on.
func sendStolen(t *testing.T, url string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url+"?key=stolen", strings.NewReader(`{}`)) // a well-formed request
	req.Header.Set("Authorization", "Bearer stolen")
	req.Header.Set("X-Api-Key", "stolen")
	req.Header.Set("Traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	req.Header.Set("Cookie", "session=stolen")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close() // read nothing
}

func TestProxyRefuses(t *testing.T) {
	for _, s := range []Settings{{Provider: "acme", Key: "test-key"}, {Provider: "openai"}} {
		if _, err := Proxy(s); err == nil {
			t.Errorf("Proxy(%+v) = nil error", s)
		}
	}
}

func TestProxyAnswersBadGatewayWhenTheProviderIsDown(t *testing.T) {
	handler, err := Proxy(Settings{Provider: "openai", Key: "test-key", URL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(`{}`)))
	if w.Code != http.StatusBadGateway {
		t.Errorf("the proxy answered %d, want %d", w.Code, http.StatusBadGateway)
	}
}
