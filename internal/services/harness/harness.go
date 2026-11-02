// Package harness works an agent's task in its pod. It asks the agent's model for its next turn, runs the tools the
// model calls, and records each step in the task's transcript through Cobranch's API before it takes the next, so a pod
// that restarts rebuilds the conversation from the transcript and carries on. Cobranch's API, the model and the tools
// are its ports, which adapters implement: the loop is the same whatever the provider.
package harness

import (
	"context"
	"encoding/json"
	"errors"

	"go.opentelemetry.io/otel"

	"cobranch.dev/internal/services/agents"
)

var tracer = otel.Tracer("cobranch.dev/internal/services/harness")

// Limits of a task's work.
const (
	// MaxOutput is the most bytes of a tool's output that the transcript keeps and the model sees; a result says so when
	// it cut the rest.
	MaxOutput = 64 << 10
	// DefaultMaxTurns is the most turns of its model a task takes, unless the harness is told otherwise.
	DefaultMaxTurns = 200
)

// ErrRefused is Cobranch refusing the task's token: the task ended, someone cancelled it, or the token expired.
var ErrRefused = errors.New("cobranch refused the task's token")

// ErrModelRefused is the model's provider refusing a request it would refuse again, such as one for a model it doesn't
// know or with a key it doesn't take, unlike a failure that a retry may get past.
var ErrModelRefused = errors.New("the model's provider refused the request")

// Cobranch is the API the harness works its task through, as the task's own token.
type Cobranch interface {
	// Work answers the task the token works.
	Work(ctx context.Context) (agents.Work, error)
	// Transcript lists every entry of the organization's task, in order.
	Transcript(ctx context.Context, org string, number int64) ([]agents.Entry, error)
	// Append records e, the next entry of the organization's task, and answers it; an entry already numbered e.Seq
	// answers that one.
	Append(ctx context.Context, org string, number int64, e agents.Entry) (agents.Entry, error)
}

// Model is the agent's model, behind its provider's adapter.
type Model interface {
	// Next asks the model for its next turn of the conversation r holds.
	Next(ctx context.Context, r Request) (Turn, error)
}

// Tool is a tool the model may call.
type Tool interface {
	// Spec describes the tool to the model.
	Spec() Spec
	// Run runs the call and answers its output for the model. An error is shown to the model as the call's failure.
	Run(ctx context.Context, c Call) (string, error)
}

// Spec describes a tool to the model: its name, what it does, and the JSON Schema of the object its arguments are.
type Spec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Role is who speaks a message of the conversation.
type Role string

// The roles.
const (
	// RoleUser speaks for the task: its instructions and the tools' results.
	RoleUser Role = "user"
	// RoleAssistant is the model.
	RoleAssistant Role = "assistant"
)

// Request is what the model is asked for its next turn: the system prompt, the conversation so far, which ends with
// one of RoleUser's messages, and the tools it may call.
type Request struct {
	System   string
	Messages []Message
	Tools    []Spec
}

// Message is a message of the conversation: RoleUser's text and the results of the calls of the turn before it, or
// RoleAssistant's text and calls.
type Message struct {
	Role    Role
	Text    string
	Calls   []Call
	Results []Result
}

// Turn is a turn of the model, as a turn entry of the transcript records it.
type Turn struct {
	Text  string `json:"text,omitempty"`
	Calls []Call `json:"calls,omitempty"`
	// Stop is why the model ended its turn, in its provider's words.
	Stop string `json:"stop,omitempty"`
	// Signature is what the provider asks to be sent back with the turn's text, opaque; empty for most.
	Signature string `json:"signature,omitempty"`
}

// Call is a call of a tool that a turn asks for.
type Call struct {
	// ID is the call's ID in the conversation, which its result names.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Arguments are a JSON object, which the tool's Spec describes.
	Arguments json.RawMessage `json:"arguments"`
	// Signature is what the provider asks to be sent back with the call, opaque; empty for most.
	Signature string `json:"signature,omitempty"`
	// Key is the call's own ID, a UUID the harness gives it as it records the turn: a tool that changes something
	// outside the pod names it, so that running the call again after a restart changes nothing more.
	Key string `json:"key,omitempty"`
	// Dropped says the turn was too large to record whole, so the call's arguments were left out and it isn't run.
	Dropped bool `json:"dropped,omitempty"`
}

// Result is a call's result, as the model sees it.
type Result struct {
	CallID string
	Name   string
	Text   string
	// Error says the call failed, Text saying why.
	Error bool
}
