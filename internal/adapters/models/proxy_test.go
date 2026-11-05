package models

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/harness"
)

func TestProxy(t *testing.T) {
	tt := []struct {
		provider, wantPath string
		wantHeader         [2]string
		answer             string
		wantInput          int64
		wantOutput         int64
	}{
		{provider: "anthropic", wantPath: "/base/v1/messages", wantHeader: [2]string{"x-api-key", "test-key"},
			answer:    `{"content":[{"type":"text","text":"Done."}],"usage":{"input_tokens":100,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"output_tokens":40}}`,
			wantInput: 150, wantOutput: 40},
		{provider: "xai", wantPath: "/base/chat/completions", wantHeader: [2]string{"Authorization", "Bearer test-key"},
			answer:    `{"choices":[{"message":{"role":"assistant","content":"Done."}}],"usage":{"prompt_tokens":100,"completion_tokens":40}}`,
			wantInput: 100, wantOutput: 40},
		{provider: "google", wantPath: "/base/v1beta/models/test-model:generateContent", wantHeader: [2]string{"x-goog-api-key", "test-key"},
			answer:    `{"candidates":[{"content":{"role":"model","parts":[{"text":"Done."}]}}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":30,"thoughtsTokenCount":10}}`,
			wantInput: 100, wantOutput: 40},
	}
	for _, tc := range tt {
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			upstream := fakeProvider(t, tc.wantPath, tc.wantHeader, tc.answer)
			defer upstream.Close()
			meter := &fakeMeter{}
			handler, err := Proxy(Settings{Provider: tc.provider, Model: "test-model", Key: "test-key", URL: upstream.URL + "/base/"}, meter)
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
			if got := meter.reports(); len(got) != 2 || got[0].Model != "test-model" || got[0].Input != tc.wantInput || got[0].Output != tc.wantOutput ||
				got[0].ID == uuid.Nil || got[0].ID == got[1].ID {
				t.Errorf("the proxy reported %+v, want each turn once, %d tokens read and %d written", got, tc.wantInput, tc.wantOutput)
			}
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

// sendStolen sends url a turn with keys, trace context and a cookie of its own, which the proxy must not send on.
func sendStolen(t *testing.T, url string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url+"?key=stolen", strings.NewReader(`{"model":"test-model"}`)) // a well-formed request
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
	tt := []struct {
		name  string
		s     Settings
		meter Meter
	}{
		{name: "a provider Cobranch doesn't know", s: Settings{Provider: "acme", Model: "test-model", Key: "test-key"}, meter: &fakeMeter{}},
		{name: "no key", s: Settings{Provider: "openai", Model: "test-model"}, meter: &fakeMeter{}},
		{name: "no model", s: Settings{Provider: "openai", Key: "test-key"}, meter: &fakeMeter{}},
		{name: "no meter", s: Settings{Provider: "openai", Model: "test-model", Key: "test-key"}},
	}
	for _, tc := range tt {
		if _, err := Proxy(tc.s, tc.meter); err == nil {
			t.Errorf("%s: Proxy() = nil error", tc.name)
		}
	}
}

func TestProxyAnswersBadGatewayWhenTheProviderIsDown(t *testing.T) {
	handler, err := Proxy(Settings{Provider: "openai", Model: "test-model", Key: "test-key", URL: "http://127.0.0.1:1"}, &fakeMeter{})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(`{"model":"test-model"}`)))
	if w.Code != http.StatusBadGateway {
		t.Errorf("the proxy answered %d, want %d", w.Code, http.StatusBadGateway)
	}
}

// TestProxyMeters walks one task's proxy through its turns, in order: each step starts where the last left it.
func TestProxyMeters(t *testing.T) {
	const turn = `{"model":"test-model","messages":[]}`
	failed := errors.New("the API is rolling out")
	steps := []struct {
		name         string
		method, path string
		body         string
		meter        []error // what the meter answers its next calls, after which it records them
		end          bool    // whether the meter's next recorded call ends the task
		status       int     // what the provider answers, 200 for 0
		wantStatus   int
		wantSent     bool
		wantReports  int
	}{
		{name: "a turn", method: http.MethodPost, path: "/v1/messages", body: turn, wantStatus: http.StatusOK, wantSent: true, wantReports: 1},
		{name: "another path", method: http.MethodPost, path: "/v1/models", body: turn, wantStatus: http.StatusNotFound},
		{name: "a read", method: http.MethodGet, path: "/v1/messages", wantStatus: http.StatusNotFound},
		{name: "another model", method: http.MethodPost, path: "/v1/messages", body: `{"model":"bigger-model"}`, wantStatus: http.StatusForbidden},
		{name: "a stream", method: http.MethodPost, path: "/v1/messages", body: `{"model":"test-model","stream":true}`, wantStatus: http.StatusBadRequest},
		{name: "not JSON", method: http.MethodPost, path: "/v1/messages", body: `model=test-model`, wantStatus: http.StatusBadRequest},
		{name: "a turn the provider fails isn't reported", method: http.MethodPost, path: "/v1/messages", body: turn, status: http.StatusInternalServerError,
			wantStatus: http.StatusInternalServerError, wantSent: true},
		{name: "a turn whose report fails still answers", method: http.MethodPost, path: "/v1/messages", body: turn, meter: []error{failed},
			wantStatus: http.StatusOK, wantSent: true, wantReports: 1},
		{name: "the next turn waits while the report still fails", method: http.MethodPost, path: "/v1/messages", body: turn, meter: []error{failed},
			wantStatus: http.StatusServiceUnavailable, wantReports: 1},
		{name: "the next turn goes once the report does", method: http.MethodPost, path: "/v1/messages", body: turn,
			wantStatus: http.StatusOK, wantSent: true, wantReports: 2},
		{name: "the turn that reaches the cap answers", method: http.MethodPost, path: "/v1/messages", body: turn, end: true,
			wantStatus: http.StatusOK, wantSent: true, wantReports: 1},
		{name: "no turn goes once the task has ended", method: http.MethodPost, path: "/v1/messages", body: turn, wantStatus: http.StatusPaymentRequired},
	}
	provider := &compressingProvider{answer: `{"usage":{"input_tokens":100,"output_tokens":10}}`}
	upstream := httptest.NewServer(provider)
	defer upstream.Close()
	meter := &fakeMeter{}
	handler, err := Proxy(Settings{Provider: "anthropic", Model: "test-model", Key: "test-key", URL: upstream.URL}, meter)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		meter.script(s.meter, s.end)
		provider.reset(s.status)
		before := len(meter.reports())
		req := httptest.NewRequest(s.method, s.path, strings.NewReader(s.body))
		req.Header.Set("Accept-Encoding", "gzip") // as the harness's client asks
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		reports := meter.reports()[before:]
		if w.Code != s.wantStatus || provider.sent() != s.wantSent || len(reports) != s.wantReports {
			t.Fatalf("%s: answered %d, sent on %t, reported %+v; want %d, %t, %d reports", s.name, w.Code, provider.sent(), reports,
				s.wantStatus, s.wantSent, s.wantReports)
		}
		for _, u := range reports {
			if u.Input != 100 || u.Output != 10 || u.Model != "test-model" {
				t.Errorf("%s: reported %+v, want the turn's tokens", s.name, u)
			}
		}
	}
	got := meter.reports()
	// The report that failed twice went a third time, as the same call, so that it counts once.
	if failedID := got[len(got)-5].ID; got[len(got)-4].ID != failedID || got[len(got)-3].ID != failedID || got[len(got)-2].ID == failedID {
		t.Errorf("reports = %+v, want the failed one repeated under its ID before the next turn's", got)
	}
}

// TestProxyStopsAtTheEndOfItsTask covers a task that ends other than at its cap, cancelled or with its token refused: the
// report of the turn on its way then learns of it, and that turn's answer is withheld, as every turn after is refused.
func TestProxyStopsAtTheEndOfItsTask(t *testing.T) {
	turns := []struct {
		name       string
		wantStatus int
		wantSent   bool
	}{
		{name: "the turn on its way is withheld", wantStatus: http.StatusPaymentRequired, wantSent: true},
		{name: "the next goes nowhere", wantStatus: http.StatusPaymentRequired},
	}
	for _, ended := range []error{agents.ErrEnded, harness.ErrRefused} {
		t.Run(ended.Error(), func(t *testing.T) {
			provider := &compressingProvider{answer: `{"usage":{"input_tokens":1}}`}
			upstream := httptest.NewServer(provider)
			defer upstream.Close()
			meter := &fakeMeter{}
			handler, err := Proxy(Settings{Provider: "openai", Model: "test-model", Key: "test-key", URL: upstream.URL}, meter)
			if err != nil {
				t.Fatal(err)
			}
			for _, turn := range turns {
				meter.script([]error{ended}, false)
				provider.reset(0)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(`{"model":"test-model"}`)))
				if w.Code != turn.wantStatus || provider.sent() != turn.wantSent || strings.Contains(w.Body.String(), "usage") {
					t.Errorf("%s: answered %d %q, sent on %t; want %d without the answer, sent on %t", turn.name, w.Code, w.Body.String(),
						provider.sent(), turn.wantStatus, turn.wantSent)
				}
			}
		})
	}
}

func TestUsage(t *testing.T) {
	tt := []struct {
		provider, answer string
		wantIn, wantOut  int64
		wantErr          bool
	}{
		{provider: "anthropic", answer: `{"usage":{"input_tokens":1,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"output_tokens":4}}`,
			wantIn: 6, wantOut: 4},
		{provider: "google", answer: `{"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":6,"thoughtsTokenCount":7}}`, wantIn: 5, wantOut: 13},
		{provider: "deepseek", answer: `{"usage":{"prompt_tokens":8,"completion_tokens":9}}`, wantIn: 8, wantOut: 9},
		{provider: "openai", answer: `{"choices":[]}`},
		{provider: "openai", answer: `not JSON`, wantErr: true},
	}
	for _, tc := range tt {
		in, out, err := usage(tc.provider, []byte(tc.answer))
		if in != tc.wantIn || out != tc.wantOut || (err != nil) != tc.wantErr {
			t.Errorf("usage(%s, %s) = %d, %d, %v; want %d, %d", tc.provider, tc.answer, in, out, err, tc.wantIn, tc.wantOut)
		}
	}
}

// fakeMeter records the reports it takes, answering as its script says.
type fakeMeter struct {
	mu   sync.Mutex
	got  []agents.Usage
	errs []error
	end  bool
}

// script has the meter fail its next calls with errs, and then end the task on the next it records, when end.
func (f *fakeMeter) script(errs []error, end bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs, f.end = errs, end
}

func (f *fakeMeter) RecordSpend(_ context.Context, u agents.Usage) (agents.Spend, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, u)
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return agents.Spend{}, err
	}
	return agents.Spend{Input: u.Input, Output: u.Output, Ended: f.end}, nil
}

// reports are the reports the meter took, in order.
func (f *fakeMeter) reports() []agents.Usage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agents.Usage(nil), f.got...)
}

// compressingProvider answers each request with its status and answer, compressed when the request asks for that, as
// providers do.
type compressingProvider struct {
	mu     sync.Mutex
	status int
	asked  bool
	answer string
}

// reset forgets what the provider was sent, and has it answer status next, 200 for 0.
func (p *compressingProvider) reset(status int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status, p.asked = max(status, http.StatusOK), false
}

// sent reports whether a request reached the provider since its reset.
func (p *compressingProvider) sent() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.asked
}

func (p *compressingProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.asked = true
	status := max(p.status, http.StatusOK)
	p.mu.Unlock()
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, p.answer) // the test fails on what the proxy reads
		return
	}
	w.Header().Set("Content-Encoding", "gzip")
	w.WriteHeader(status)
	z := gzip.NewWriter(w)
	_, _ = io.WriteString(z, p.answer) // as above
	_ = z.Close()                      // as above
}
