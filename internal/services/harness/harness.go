// Package harness works an agent's task in its pod. It asks the agent's model for its next turn, runs the tools the
// model calls, and records each step in the task's transcript through Cobranch's API before it takes the next, so a pod
// that restarts rebuilds the conversation from the transcript and carries on. Cobranch's API, the model and the tools
// are its ports, which adapters implement: the loop is the same whatever the provider.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
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
	// Thread lists the messages of a thread of one of the organization's chats, in the order they were posted.
	Thread(ctx context.Context, org string, chat Chat, threadID uuid.UUID) ([]ChatMessage, error)
	// StartThread starts thread id of one of the organization's chats with its first message; starting it again
	// changes nothing.
	StartThread(ctx context.Context, org string, chat Chat, id uuid.UUID, body string) error
	// Post posts message id to a thread of one of the organization's chats; posting it again changes nothing.
	Post(ctx context.Context, org string, chat Chat, threadID, id uuid.UUID, body string) error
}

// Chat names one of an organization's chats: a branch's, by its repository and name, or an issue's, by its number.
type Chat struct {
	Repo, Branch string
	Issue        int64
}

// ChatMessage is a message of a chat's thread, as the model reads it.
type ChatMessage struct {
	ID uuid.UUID
	// Author is who posted it; empty once they are gone.
	Author string
	// Task is the number of the agent's task it came from; 0 for a person's message.
	Task    int64
	At      time.Time
	Body    string
	Deleted bool
}

// Checkout readies the task's workspace before the harness works it.
type Checkout interface {
	// Prepare readies the workspace for the task: Git's identity, the agent's, and its credentials; and, when the task
	// names a repository that the workspace doesn't hold yet, the repository, cloned on the branch its work goes to
	// (agents.Work.Branch). It answers the clone the workspace holds.
	Prepare(ctx context.Context, w agents.Work) (Clone, error)
}

// Clone is the clone of the task's repository that its workspace holds.
type Clone struct {
	// Branch is the branch the clone is on, which the task's work goes to; empty when the workspace holds no clone,
	// for a task without a repository or an installation without a Git server.
	Branch string
	// Commit is the commit the clone is at, detached, for a task that works at one, such as a review; empty for a clone
	// on Branch's head.
	Commit string
	// Fresh is whether Prepare just made the clone.
	Fresh bool
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

// Kind fits the harness to one kind of task, such as the AI reviewer's reviews: what its tasks get beside the agent's
// own tools and instructions, and what one must have done before it succeeds.
type Kind interface {
	// Prepare readies the task w, as each pod starts it, and answers the kit it works with; or, through Kit.End, how it
	// ends without asking the model at all. An error is one a restart may get past.
	Prepare(ctx context.Context, w agents.Work) (Kit, error)
}

// Kit is what a Kind gives a task.
type Kit struct {
	// Tools are offered beside the agent's own.
	Tools []Tool
	// Instructions follow the agent's own in the system prompt.
	Instructions string
	// End, when set, ends the task as it says before the model is asked: its kind did the task itself.
	End *agents.StateChange
	// Unfinished answers what the task still has to do before it may succeed, empty once nothing; finish's succeeded
	// outcome is refused until it is. An error is one a restart may get past. Nil for nothing.
	Unfinished func(ctx context.Context) (string, error)
	// Timeout is how long the task may run from its start, after which it fails; 0 for as long as the controller lets
	// its pod run.
	Timeout time.Duration
	// Quiet keeps the task out of its chat, without its tools or a prompt that sends it there: for a kind whose work
	// reaches people another way, such as a review, whose comments open threads of the branch's chat themselves.
	Quiet bool
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
	// Signature is what the provider asked to be sent back with the assistant's text, opaque; empty for most.
	Signature string
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
