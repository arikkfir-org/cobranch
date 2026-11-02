package models

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/harness"
)

func TestProviders(t *testing.T) {
	// A provider's name is a row's key in model_providers, which takes these alone.
	name := regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	got := Providers()
	if !slices.IsSorted(got) || len(slices.Compact(slices.Clone(got))) != len(got) {
		t.Errorf("Providers() = %v, want them sorted and each once", got)
	}
	for _, p := range got {
		if !name.MatchString(p) {
			t.Errorf("Providers() names %q, which model_providers refuses", p)
		}
	}
	for _, p := range got {
		if _, err := New(Settings{Provider: p, Model: "test-model", Key: "test-key"}); err != nil {
			t.Errorf("New(%s) = %v", p, err)
		}
	}
	for _, s := range []Settings{{Provider: "acme", Model: "test-model", Key: "test-key"}, {Provider: "openai", Key: "test-key"}, {Provider: "openai", Model: "test-model"}} {
		if _, err := New(s); err == nil {
			t.Errorf("New(%+v) = nil, want an error", s)
		}
	}
}

// conversation is a request with a turn of calls behind it, one of whose results failed, and text after them.
var conversation = harness.Request{System: "Be brief.", Tools: []harness.Spec{{Name: "echo", Description: "Echoes.", Parameters: json.RawMessage(`{"type":"object"}`)}},
	Messages: []harness.Message{
		{Role: harness.RoleUser, Text: "# Sweep"},
		{Role: harness.RoleAssistant, Text: "Looking.", Signature: "sig0", Calls: []harness.Call{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"text":"hi"}`), Signature: "sig1"}}},
		{Role: harness.RoleUser, Text: "Also this.", Results: []harness.Result{{CallID: "c1", Name: "echo", Text: "hi"}, {CallID: "c2", Name: "echo", Text: "boom", Error: true}}},
	}}

// providerCase is a provider asked for conversation's next turn: what it is sent, what it answers, and the turn read
// from that.
type providerCase struct {
	provider   string
	wantPath   string
	wantHeader [2]string
	wantBody   string
	answer     string
	want       harness.Turn
}

func TestNext(t *testing.T) {
	finish := harness.Call{ID: "c3", Name: "finish", Arguments: json.RawMessage(`{"outcome":"succeeded"}`)}
	tt := []providerCase{
		{provider: "anthropic", wantPath: "/v1/messages", wantHeader: [2]string{"x-api-key", "test-key"},
			wantBody: `{"model":"test-model","max_tokens":8192,"system":"Be brief.","messages":[
				{"role":"user","content":[{"type":"text","text":"# Sweep"}]},
				{"role":"assistant","content":[{"type":"text","text":"Looking."},{"type":"tool_use","id":"c1","name":"echo","input":{"text":"hi"}}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"hi"},{"type":"tool_result","tool_use_id":"c2","content":"boom","is_error":true},{"type":"text","text":"Also this."}]}],
				"tools":[{"name":"echo","description":"Echoes.","input_schema":{"type":"object"}}]}`,
			answer: `{"content":[{"type":"text","text":"Done."},{"type":"tool_use","id":"c3","name":"finish","input":{"outcome":"succeeded"}}],"stop_reason":"tool_use"}`,
			want:   harness.Turn{Text: "Done.", Calls: []harness.Call{finish}, Stop: "tool_use"}},
		{provider: "openai", wantPath: "/chat/completions", wantHeader: [2]string{"Authorization", "Bearer test-key"},
			wantBody: `{"model":"test-model","messages":[{"role":"system","content":"Be brief."},{"role":"user","content":"# Sweep"},
				{"role":"assistant","content":"Looking.","tool_calls":[{"id":"c1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"hi\"}"}}]},
				{"role":"tool","content":"hi","tool_call_id":"c1"},{"role":"tool","content":"Error: boom","tool_call_id":"c2"},{"role":"user","content":"Also this."}],
				"tools":[{"type":"function","function":{"name":"echo","description":"Echoes.","parameters":{"type":"object"}}}]}`,
			answer: `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c3","type":"function","function":{"name":"finish","arguments":"{\"outcome\":\"succeeded\"}"}}]},"finish_reason":"tool_calls"}]}`,
			want:   harness.Turn{Calls: []harness.Call{finish}, Stop: "tool_calls"}},
		{provider: "google", wantPath: "/v1beta/models/test-model:generateContent", wantHeader: [2]string{"x-goog-api-key", "test-key"},
			wantBody: `{"systemInstruction":{"parts":[{"text":"Be brief."}]},"contents":[{"role":"user","parts":[{"text":"# Sweep"}]},
				{"role":"model","parts":[{"text":"Looking.","thoughtSignature":"sig0"},{"functionCall":{"id":"c1","name":"echo","args":{"text":"hi"}},"thoughtSignature":"sig1"}]},
				{"role":"user","parts":[{"functionResponse":{"id":"c1","name":"echo","response":{"output":"hi"}}},{"functionResponse":{"id":"c2","name":"echo","response":{"error":"boom"}}},{"text":"Also this."}]}],
				"tools":[{"functionDeclarations":[{"name":"echo","description":"Echoes.","parameters":{"type":"object"}}]}],"generationConfig":{"maxOutputTokens":8192}}`,
			answer: `{"candidates":[{"content":{"role":"model","parts":[{"text":"Weighing it.","thought":true},{"text":"Done.","thoughtSignature":"sig2"},
				{"functionCall":{"id":"c3","name":"finish","args":{"outcome":"succeeded"}},"thoughtSignature":"sig3"}]},"finishReason":"STOP"}]}`,
			want: harness.Turn{Text: "Done.", Signature: "sig2", Stop: "STOP", Calls: []harness.Call{{ID: "c3", Name: "finish", Arguments: finish.Arguments, Signature: "sig3"}}}},
	}
	for _, tc := range tt {
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body) // a failed read fails the comparison below
				if r.Method != http.MethodPost || r.URL.Path != tc.wantPath || r.Header.Get(tc.wantHeader[0]) != tc.wantHeader[1] || !sameJSON(t, body, tc.wantBody) {
					t.Errorf("%s %s with %s: %q sent %s", r.Method, r.URL.Path, tc.wantHeader[0], r.Header.Get(tc.wantHeader[0]), body)
				}
				_, _ = io.WriteString(w, tc.answer) // the test fails on what the model reads
			}))
			defer srv.Close()
			m, err := New(Settings{Provider: tc.provider, Model: "test-model", Key: "test-key", URL: srv.URL + "/"})
			if err != nil {
				t.Fatal(err)
			}
			got, err := m.Next(context.Background(), conversation)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Next() = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

// sameJSON reports whether got and want are the same JSON value.
func sameJSON(t *testing.T, got []byte, want string) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("the wanted JSON: %v", err)
	}
	return reflect.DeepEqual(g, w)
}

func TestNextReadsWhatProvidersVary(t *testing.T) {
	tt := []struct {
		name, provider, answer string
		check                  func(t *testing.T, got harness.Turn)
	}{
		{name: "a call without an ID", provider: "google", answer: `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"finish"}}]},"finishReason":"STOP"}]}`,
			check: func(t *testing.T, got harness.Turn) {
				if len(got.Calls) != 1 || !strings.HasPrefix(got.Calls[0].ID, "call_") || string(got.Calls[0].Arguments) != `{}` {
					t.Errorf("Next() = %+v, want a call with an ID of its own and no arguments", got)
				}
			}},
		{name: "arguments that aren't JSON", provider: "openai", answer: `{"choices":[{"message":{"tool_calls":[{"id":"c","function":{"name":"echo","arguments":"{\"text\":"}}]}}]}`,
			check: func(t *testing.T, got harness.Turn) {
				if len(got.Calls) != 1 || string(got.Calls[0].Arguments) != `"{\"text\":"` {
					t.Errorf("Next() = %+v, want the arguments as a string", got)
				}
			}},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, tc.answer) }))
			defer srv.Close()
			m, err := New(Settings{Provider: tc.provider, Model: "test-model", Key: "test-key", URL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			got, err := m.Next(context.Background(), harness.Request{Messages: []harness.Message{{Role: harness.RoleUser, Text: "Go."}}})
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, got)
		})
	}
}

func TestNextRefusesATurnTheProviderBlocked(t *testing.T) {
	// Asked again, a blocked turn is blocked again: the harness fails the task on ErrModelRefused instead of nudging.
	tt := []struct {
		name, provider, answer, want string
	}{
		{name: "a blocked prompt", provider: "google", answer: `{"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"}}`,
			want: "the model's provider refused the request (google): it stopped the turn: the prompt was blocked: PROHIBITED_CONTENT"},
		{name: "a blocked prompt without a reason", provider: "google", answer: `{}`,
			want: "the model's provider refused the request (google): it stopped the turn: the prompt was blocked: no reason given"},
		{name: "a candidate stopped for safety", provider: "google", answer: `{"candidates":[{"content":{"parts":[{"text":"Par"}]},"finishReason":"SAFETY"}]}`,
			want: "the model's provider refused the request (google): it stopped the turn: SAFETY"},
		{name: "a filtered completion", provider: "openai", answer: `{"choices":[{"message":{"content":"Par"},"finish_reason":"content_filter"}]}`,
			want: "the model's provider refused the request (openai): it stopped the turn: content_filter"},
		{name: "a refusal", provider: "anthropic", answer: `{"content":[],"stop_reason":"refusal"}`,
			want: "the model's provider refused the request (anthropic): it stopped the turn: refusal"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, tc.answer) }))
			defer srv.Close()
			m, err := New(Settings{Provider: tc.provider, Model: "test-model", Key: "test-key", URL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			got, err := m.Next(context.Background(), harness.Request{Messages: []harness.Message{{Role: harness.RoleUser, Text: "Go."}}})
			if !errors.Is(err, harness.ErrModelRefused) || err.Error() != tc.want {
				t.Errorf("Next() = %+v, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestNextRecordsAGenAISpan(t *testing.T) {
	// Not parallel: it sets the tracer provider every test of the package uses, and tells its spans by their models.
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	tt := []struct {
		name, provider, model, answer string
		want                          map[string]string
		wantStatus                    codes.Code
	}{
		{name: "a turn", provider: "anthropic", model: "traced-anthropic",
			answer: `{"model":"traced-anthropic-1","content":[{"type":"text","text":"Done."}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":3}}`,
			want: map[string]string{"gen_ai.operation.name": "chat", "gen_ai.provider.name": "anthropic", "gen_ai.request.model": "traced-anthropic",
				"gen_ai.request.max_tokens": "8192", "gen_ai.response.model": "traced-anthropic-1", "gen_ai.response.finish_reasons": `["end_turn"]`,
				"gen_ai.usage.input_tokens": "12", "gen_ai.usage.output_tokens": "3"},
			wantStatus: codes.Unset},
		{name: "a turn of an OpenAI-compatible provider", provider: "xai", model: "traced-xai",
			answer:     `{"model":"traced-xai","choices":[{"message":{"content":"Done."},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`,
			want:       map[string]string{"gen_ai.provider.name": "x_ai", "gen_ai.response.model": "traced-xai", "gen_ai.usage.input_tokens": "7", "gen_ai.usage.output_tokens": "2"},
			wantStatus: codes.Unset},
		{name: "a blocked prompt", provider: "google", model: "traced-google",
			answer:     `{"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":5},"modelVersion":"traced-google-1"}`,
			want:       map[string]string{"gen_ai.provider.name": "gcp.gemini", "gen_ai.usage.input_tokens": "5", "gen_ai.usage.output_tokens": "0"},
			wantStatus: codes.Error},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, tc.answer) }))
			defer srv.Close()
			m, err := New(Settings{Provider: tc.provider, Model: tc.model, Key: "test-key", URL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			_, _ = m.Next(context.Background(), harness.Request{Messages: []harness.Message{{Role: harness.RoleUser, Text: "Go."}}}) // the span tells
			var span sdktrace.ReadOnlySpan
			for _, s := range recorder.Ended() {
				if s.Name() == "chat "+tc.model {
					span = s
				}
			}
			if span == nil {
				t.Fatalf("no span chat %s among %d", tc.model, len(recorder.Ended()))
			}
			got := map[string]string{}
			for _, kv := range span.Attributes() {
				got[string(kv.Key)] = kv.Value.Emit()
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q (%v)", k, got[k], v, got)
				}
			}
			if span.SpanKind() != trace.SpanKindClient || span.Status().Code != tc.wantStatus {
				t.Errorf("span of kind %v, status %v; want a client's, %v", span.SpanKind(), span.Status(), tc.wantStatus)
			}
		})
	}
}

func TestMessagesTheProvidersRefuseEmpty(t *testing.T) {
	// A turn with neither text nor calls, or arguments that aren't an object, would be refused as they are.
	m := harness.Message{Role: harness.RoleAssistant}
	if got := anthropicMessageOf(m); len(got.Content) != 1 || got.Content[0].Text != emptyTurn {
		t.Errorf("anthropicMessageOf(an empty turn) = %+v", got)
	}
	if got := geminiContentOf(m); len(got.Parts) != 1 || got.Parts[0].Text != emptyTurn {
		t.Errorf("geminiContentOf(an empty turn) = %+v", got)
	}
	called := harness.Message{Role: harness.RoleAssistant, Calls: []harness.Call{{ID: "c", Name: "echo", Arguments: json.RawMessage(`"{\"text\":"`)}}}
	if got := openAIMessagesOf(called); got[0].Content != nil || got[0].ToolCalls[0].Function.Arguments != `{}` {
		t.Errorf("openAIMessagesOf(arguments that aren't an object) = %+v", got)
	}
}

// statusCase is a provider that answers statuses in turn, the last one again once they run out.
type statusCase struct {
	name      string
	statuses  []int
	wantCalls int32
	wantErr   error
	wantText  string
}

func TestRetries(t *testing.T) {
	tt := []statusCase{
		{name: "an overload, then an answer", statuses: []int{529, http.StatusServiceUnavailable, http.StatusOK}, wantCalls: 3},
		{name: "a rate limit for good", statuses: []int{http.StatusTooManyRequests}, wantCalls: attempts, wantText: "anthropic answered 429: Slow down."},
		{name: "a model the provider doesn't know", statuses: []int{http.StatusNotFound}, wantCalls: 1, wantErr: harness.ErrModelRefused,
			wantText: "the model's provider refused the request (anthropic, 404): Slow down."},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				status := tc.statuses[min(int(calls.Add(1)), len(tc.statuses))-1]
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"Done."}],"stop_reason":"end_turn"}`) // the test fails on what the model reads
					return
				}
				_, _ = io.WriteString(w, `{"type":"error","error":{"type":"error","message":"Slow down."}}`) // as above
			}))
			defer srv.Close()
			m, err := New(Settings{Provider: "anthropic", Model: "test-model", Key: "test-key", URL: srv.URL, backoff: time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			got, err := m.Next(context.Background(), harness.Request{Messages: []harness.Message{{Role: harness.RoleUser, Text: "Go."}}})
			switch {
			case calls.Load() != tc.wantCalls:
				t.Errorf("asked %d times, want %d", calls.Load(), tc.wantCalls)
			case tc.wantText == "" && (err != nil || got.Text != "Done."):
				t.Errorf("Next() = %+v, %v", got, err)
			case tc.wantText != "" && (err == nil || err.Error() != tc.wantText || (tc.wantErr != nil && !errors.Is(err, tc.wantErr))):
				t.Errorf("Next() = %v, want %q", err, tc.wantText)
			case err != nil && strings.Contains(err.Error(), "test-key"):
				t.Errorf("Next() = %v, which names the key", err)
			}
		})
	}
}

func TestRetriesStopWithTheContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	m, err := New(Settings{Provider: "openai", Model: "test-model", Key: "test-key", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := m.Next(ctx, harness.Request{}); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Errorf("Next() = %v after %s, want the context's end, at once", err, time.Since(start))
	}
}

func TestRefusalWithoutAMessage(t *testing.T) {
	c := caller{provider: "xai"}
	err := c.refusal(http.StatusBadRequest, bytes.Repeat([]byte("x"), 500))
	if !errors.Is(err, harness.ErrModelRefused) || !strings.HasSuffix(err.Error(), "(xai, 400): "+strings.Repeat("x", 300)) {
		t.Errorf("refusal() = %v", err)
	}
}
