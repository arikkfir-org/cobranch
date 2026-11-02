// Package models holds the model providers an agent's harness speaks to, each an adapter of its own behind one model
// interface, and none called unless an organization added its provider.
package models

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	// The newest conventions that still hold GenAI's; later versions left them out.
	genai "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/harness"
)

var tracer = otel.Tracer("cobranch.dev/internal/adapters/models")

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
		return model{anthropic{caller: c, url: base(s.URL, "https://api.anthropic.com"), settings: s}, s}, nil
	case "google":
		return model{gemini{caller: c, url: base(s.URL, "https://generativelanguage.googleapis.com"), settings: s}, s}, nil
	case "deepseek":
		return model{openAI{caller: c, url: base(s.URL, "https://api.deepseek.com"), settings: s}, s}, nil
	case "xai":
		return model{openAI{caller: c, url: base(s.URL, "https://api.x.ai/v1"), settings: s}, s}, nil
	default:
		return model{openAI{caller: c, url: base(s.URL, "https://api.openai.com/v1"), settings: s}, s}, nil
	}
}

// adapter is a provider's adapter: it asks the model for its next turn in its provider's protocol.
type adapter interface {
	chat(ctx context.Context, r harness.Request) (harness.Turn, answered, error)
}

// answered is what a provider's answer says beside the turn: the model that answered, the tokens it took in and gave
// out, and why the provider stopped the turn for its content, when it did.
type answered struct {
	model         string
	input, output int
	blocked       string
}

// genAIProviders are the providers as OpenTelemetry's GenAI conventions name them.
var genAIProviders = map[string]attribute.KeyValue{
	"anthropic": genai.GenAIProviderNameAnthropic,
	"deepseek":  genai.GenAIProviderNameDeepseek,
	"google":    genai.GenAIProviderNameGCPGemini,
	"openai":    genai.GenAIProviderNameOpenAI,
	"xai":       genai.GenAIProviderNameXAI,
}

// model is a provider's model, behind its adapter (harness.Model).
type model struct {
	adapter
	settings Settings
}

// Next asks the model for its next turn (harness.Model), in a span of OpenTelemetry's GenAI conventions that records
// the tokens it took and how it ended. A turn the provider stopped for its content fails with harness.ErrModelRefused
// and the provider's reason, since asking again would be stopped again.
func (m model) Next(ctx context.Context, r harness.Request) (harness.Turn, error) {
	ctx, span := tracer.Start(ctx, "chat "+m.settings.Model, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		genai.GenAIOperationNameChat, genAIProviders[m.settings.Provider], genai.GenAIRequestModel(m.settings.Model),
		genai.GenAIRequestMaxTokens(m.settings.MaxTokens)))
	defer span.End()
	t, meta, err := m.chat(ctx, r)
	span.SetAttributes(genai.GenAIUsageInputTokens(meta.input), genai.GenAIUsageOutputTokens(meta.output))
	if meta.model != "" {
		span.SetAttributes(genai.GenAIResponseModel(meta.model))
	}
	if t.Stop != "" {
		span.SetAttributes(genai.GenAIResponseFinishReasons(t.Stop))
	}
	if err == nil && meta.blocked != "" {
		err = fmt.Errorf("%w (%s): it stopped the turn: %s", harness.ErrModelRefused, m.settings.Provider, meta.blocked)
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return harness.Turn{}, err
	}
	return t, nil
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
