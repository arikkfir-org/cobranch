package models

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/harness"
)

// Meter records what each call of a task's model read and wrote, which Cobranch prices and holds against the task's
// cap, and answers what the task spent and whether it has ended; agents.ErrEnded, or harness.ErrRefused for a token
// refused, once it has.
type Meter interface {
	RecordSpend(ctx context.Context, u agents.Usage) (agents.Spend, error)
}

// metered is the model proxy's gate in front of the provider, next: it sends on only a turn of the task's model, asked whole
// rather than streamed, so that each answer says what the turn read and wrote, and reports that before the harness
// reads the answer. A report that fails is kept and sent again before the next turn, which waits for it, so a task
// never runs more than one turn ahead of what it spent; once the task has ended, at its cap or otherwise, no turn goes.
// A task that ends otherwise, cancelled or timed out, is learnt of from the report of the turn on its way then, whose
// answer is withheld: the provider charged for it, but the harness never acts on it.
type metered struct {
	provider, model string
	meter           Meter
	next            http.Handler
	// mu holds one turn at a time, as the harness asks them, with what it reads and writes below.
	mu      sync.Mutex
	pending *agents.Usage
	ended   bool
}

// errEnded is a turn refused, or its answer withheld, because the task has ended.
var errEnded = errors.New("the task has ended, as it does once it reaches its cap: its model takes no more turns")

// ServeHTTP sends a turn of the task's model on, after the report of the last one, unless the task has ended.
func (m *metered) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if status, err := m.admit(r); err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if m.pending != nil {
		if err := m.report(r.Context(), *m.pending); err != nil && !m.ended {
			slog.WarnContext(r.Context(), "Reporting the last call's spend failed again", "error", err)
			http.Error(w, "Cobranch hasn't recorded what the last turn spent yet: ask again shortly.", http.StatusServiceUnavailable)
			return
		}
	}
	if m.ended {
		http.Error(w, errEnded.Error(), http.StatusPaymentRequired)
		return
	}
	m.next.ServeHTTP(w, r)
}

// admit refuses, with the status to answer, a request that isn't a turn of the task's model asked whole: another path,
// another model, or a stream, whose answer would say what it spent only piece by piece.
func (m *metered) admit(r *http.Request) (int, error) {
	if r.Method != http.MethodPost || r.URL.Path != turnPath(m.provider, m.model) {
		return http.StatusNotFound, fmt.Errorf("the model proxy sends on only a turn of %s, to %s", m.model, turnPath(m.provider, m.model))
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxAnswer+1))
	if err != nil {
		return http.StatusBadRequest, fmt.Errorf("reading the request: %w", err)
	}
	if len(body) > maxAnswer {
		return http.StatusRequestEntityTooLarge, errors.New("a turn's request is at most 32 MiB")
	}
	r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
	var asked struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	switch {
	case json.Unmarshal(body, &asked) != nil:
		return http.StatusBadRequest, errors.New("a turn's request is a JSON object")
	case asked.Stream:
		return http.StatusBadRequest, errors.New("the model proxy sends on a turn asked whole, not streamed")
	case m.provider != "google" && asked.Model != m.model:
		return http.StatusForbidden, fmt.Errorf("the task runs on %s alone", m.model)
	}
	return 0, nil
}

// turnPath is where the harness asks provider's model for a turn, as the proxy serves it.
func turnPath(provider, model string) string {
	switch provider {
	case "anthropic":
		return "/v1/messages"
	case "google":
		return "/v1beta/models/" + model + ":generateContent"
	default:
		return "/chat/completions"
	}
}

// meterAnswer reads a turn's answer, reports what it read and wrote, and puts the answer back for the harness. A report
// that fails is kept for the next turn, and the answer still goes: the provider already charged for it. A report that
// finds the task already ended withholds the answer, errEnded, so the harness takes no step past the task's end.
func (m *metered) meterAnswer(resp *http.Response) error {
	if resp.StatusCode/100 != 2 {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	_ = resp.Body.Close() // read whole, or failed: either way done with
	if err != nil {
		return fmt.Errorf("reading %s's answer: %w", m.provider, err)
	}
	if len(body) > maxAnswer {
		return fmt.Errorf("%s's answer is past 32 MiB", m.provider)
	}
	resp.Body, resp.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
	ctx := context.WithoutCancel(resp.Request.Context()) // a harness that stops listening doesn't take back what it spent
	input, output, err := usage(m.provider, body)
	if err != nil {
		slog.ErrorContext(ctx, "Reading what a turn spent failed", "provider", m.provider, "error", err)
		return nil
	}
	u := agents.Usage{ID: uuid.New(), Model: m.model, Input: input, Output: output}
	switch err := m.report(ctx, u); {
	case err != nil && m.ended:
		slog.WarnContext(ctx, "A turn answered after its task ended: its answer is withheld", "input_tokens", input,
			"output_tokens", output, "error", err)
		return errEnded
	case err != nil:
		slog.WarnContext(ctx, "Reporting a call's spend failed: it goes again before the next turn", "error", err)
		m.pending = &u
	}
	return nil
}

// report sends u, and records whether the task has ended; the pending report goes once it is sent.
func (m *metered) report(ctx context.Context, u agents.Usage) error {
	spend, err := m.meter.RecordSpend(ctx, u)
	if err != nil {
		if errors.Is(err, agents.ErrEnded) || errors.Is(err, harness.ErrRefused) {
			m.ended, m.pending = true, nil
		}
		return fmt.Errorf("reporting call %s: %w", u.ID, err)
	}
	m.ended, m.pending = spend.Ended, nil
	return nil
}

// usage reads the tokens a turn read and wrote from provider's answer: what the provider charges for, cached input and
// thinking included.
func usage(provider string, body []byte) (int64, int64, error) {
	var a struct {
		Usage struct {
			InputTokens         int64 `json:"input_tokens"`
			CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadTokens     int64 `json:"cache_read_input_tokens"`
			OutputTokens        int64 `json:"output_tokens"`
			PromptTokens        int64 `json:"prompt_tokens"`
			CompletionTokens    int64 `json:"completion_tokens"`
		} `json:"usage"`
		UsageMetadata struct {
			PromptTokenCount     int64 `json:"promptTokenCount"`
			CandidatesTokenCount int64 `json:"candidatesTokenCount"`
			ThoughtsTokenCount   int64 `json:"thoughtsTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(body, &a); err != nil {
		return 0, 0, fmt.Errorf("reading %s's answer: %w", provider, err)
	}
	u, g := a.Usage, a.UsageMetadata
	switch provider {
	case "anthropic":
		return u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens, u.OutputTokens, nil
	case "google":
		return g.PromptTokenCount, g.CandidatesTokenCount + g.ThoughtsTokenCount, nil
	default:
		return u.PromptTokens, u.CompletionTokens, nil
	}
}
