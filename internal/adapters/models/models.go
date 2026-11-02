// Package models holds the model providers an agent's harness speaks to, each an adapter of its own behind one model
// interface, and none called unless an organization added its provider.
package models

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"

	"cobranch.dev/internal/services/harness"
)

// Providers names the model providers Cobranch has adapters for: those an organization may add, by the name the API
// and the Console show.
func Providers() []string {
	return []string{"anthropic", "deepseek", "google", "openai", "xai"}
}

// Settings name the model a harness speaks to, and how it reaches it.
type Settings struct {
	// Provider is one of Providers.
	Provider string
	// Model is the model's name, as its provider names it.
	Model string
	// Key is the provider's API key.
	Key string
	// URL is where the provider's API is, when not at the provider's own address: a proxy, or another host that speaks
	// its protocol. Empty for the provider's own.
	URL string
	// MaxTokens is the most tokens a turn may take, for the providers that ask for it; DefaultMaxTokens for 0.
	MaxTokens int
	// Client sends the requests; for nil, an instrumented default client that sends no trace context, which stays within
	// Cobranch.
	Client *http.Client
	// backoff is the first wait before a retry, which doubles with each; DefaultBackoff for 0.
	backoff time.Duration
}

// Defaults of Settings.
const (
	DefaultMaxTokens = 8192
	DefaultBackoff   = time.Second
)

// New returns the model s names, behind its provider's adapter.
func New(s Settings) (harness.Model, error) {
	if !slices.Contains(Providers(), s.Provider) {
		return nil, fmt.Errorf("%q is not a model provider Cobranch knows: one of %s", s.Provider, strings.Join(Providers(), ", "))
	}
	if s.Model == "" || s.Key == "" {
		return nil, fmt.Errorf("a model of %s takes its name and an API key", s.Provider)
	}
	if s.MaxTokens <= 0 {
		s.MaxTokens = DefaultMaxTokens
	}
	if s.backoff <= 0 {
		s.backoff = DefaultBackoff
	}
	if s.Client == nil {
		s.Client = &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport, otelhttp.WithPropagators(propagation.NewCompositeTextMapPropagator()))}
	}
	c := caller{provider: s.Provider, client: s.Client, backoff: s.backoff}
	switch s.Provider {
	case "anthropic":
		return anthropic{caller: c, url: base(s.URL, "https://api.anthropic.com"), settings: s}, nil
	case "google":
		return gemini{caller: c, url: base(s.URL, "https://generativelanguage.googleapis.com"), settings: s}, nil
	case "deepseek":
		return openAI{caller: c, url: base(s.URL, "https://api.deepseek.com"), settings: s}, nil
	case "xai":
		return openAI{caller: c, url: base(s.URL, "https://api.x.ai/v1"), settings: s}, nil
	default:
		return openAI{caller: c, url: base(s.URL, "https://api.openai.com/v1"), settings: s}, nil
	}
}

// base is url without its trailing slash, or fallback for none.
func base(url, fallback string) string {
	if url == "" {
		return fallback
	}
	return strings.TrimSuffix(url, "/")
}

// object is args when they are a JSON object, and an empty object otherwise, which every provider takes: a call whose
// arguments weren't JSON is recorded with them as a string, and its tool refuses it.
func object(args json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(args, &fields) != nil || fields == nil {
		return json.RawMessage(`{}`)
	}
	return args
}

// outcome is a result's text as the providers without an error flag see it.
func outcome(r harness.Result) string {
	if r.Error {
		return "Error: " + r.Text
	}
	return r.Text
}
