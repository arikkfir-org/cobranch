package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// EntryKind is what an entry of a task's transcript records.
type EntryKind string

// The entry kinds.
const (
	// EntryInput is what the task began with, or a message a person sent it since.
	EntryInput EntryKind = "input"
	// EntryTurn is a turn of the agent's model, with the tool calls it asked for.
	EntryTurn EntryKind = "turn"
	// EntryResult is a tool's result.
	EntryResult EntryKind = "result"
	// EntryState is a change of the task's state, which appending it makes: its body is a StateChange.
	EntryState EntryKind = "state"
)

// EntryKinds are the entry kinds.
var EntryKinds = []EntryKind{EntryInput, EntryTurn, EntryResult, EntryState}

// Limits of a transcript.
const (
	// MaxEntry is the most bytes an entry's body holds: the harness cuts a tool's output to 64 KiB, and a turn holds the
	// model's text beside its calls.
	MaxEntry = 256 << 10
	// MaxEntriesPage is the most entries a page lists.
	MaxEntriesPage = 500
	// MaxSummary and MaxReason are the most characters a task's summary and the reason it failed hold.
	MaxSummary = 20000
	MaxReason  = 1000
)

// ErrTransition is a state entry that moves its task where it can't go from where it is: a task the controller
// hasn't started yet, or a move to the state it is in.
var ErrTransition = errors.New("the task can't move to that state from where it is")

// Entry is a step of a task's transcript, numbered from 1 in its task. The harness writes its body, a JSON object,
// and reads it back when its pod restarts; Cobranch reads only a state entry's.
type Entry struct {
	Seq       int
	Kind      EntryKind
	Body      json.RawMessage
	CreatedAt time.Time
}

// StateChange is a state entry's body: the state its task moves to, with the summary of a task that succeeded or the
// reason of one that failed.
type StateChange struct {
	State   TaskState `json:"state"`
	Summary string    `json:"summary,omitempty"`
	Reason  string    `json:"reason,omitempty"`
}

// From lists the states a task moves to c.State from: the harness moves a running task to waiting and back, and ends
// one that runs or waits; only the controller starts a queued one.
func (c StateChange) From() []TaskState {
	switch c.State {
	case TaskRunning:
		return []TaskState{TaskWaiting}
	case TaskWaiting:
		return []TaskState{TaskRunning}
	case TaskSucceeded, TaskFailed:
		return []TaskState{TaskRunning, TaskWaiting}
	default:
		return nil
	}
}

// ValidateEntry checks e, and answers a state entry's change: a number from 1, a kind, a body that is a JSON object
// of at most MaxEntry bytes, and for a state entry a state the harness moves a task to, with a summary of 1 to
// MaxSummary characters for succeeded and a reason of 1 to MaxReason for failed.
func ValidateEntry(e Entry) (*StateChange, error) {
	switch {
	case e.Seq < 1:
		return nil, invalid("seq", "transcript.seq_invalid", "entries are numbered from 1")
	case !slices.Contains(EntryKinds, e.Kind):
		return nil, invalid("kind", "transcript.kind_invalid", "not an entry kind")
	case len(e.Body) > MaxEntry:
		return nil, invalid("body", "transcript.body_invalid", fmt.Sprintf("a body holds at most %d bytes", MaxEntry))
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(e.Body, &object); err != nil || object == nil {
		return nil, invalid("body", "transcript.body_invalid", "a body is a JSON object")
	}
	if e.Kind != EntryState {
		return nil, nil
	}
	var c StateChange
	if err := json.Unmarshal(e.Body, &c); err != nil || c.From() == nil {
		return nil, invalid("body.state", "task.state_invalid", "a state entry moves its task to running, waiting, succeeded or failed")
	}
	var err error
	switch c.State {
	case TaskSucceeded:
		c.Summary, err = text(c.Summary, true, MaxSummary, "body.summary", "task.summary_invalid", "a summary is 1 to 20000 characters")
		c.Reason = ""
	case TaskFailed:
		c.Reason, err = text(c.Reason, true, MaxReason, "body.reason", "task.reason_invalid", "a reason is 1 to 1000 characters")
		c.Summary = ""
	default:
		c.Summary, c.Reason = "", ""
	}
	return &c, err
}

// working answers the task the request's credential works, when it is a task token of v's.
func working(ctx context.Context, v Viewer) (uuid.UUID, bool) {
	a, ok := auth.Viewer(ctx)
	if !ok || a.Credential.Kind != auth.CredentialTaskToken || a.User.ID != v.ID {
		return uuid.Nil, false
	}
	return a.Credential.Task.ID, true
}

// Work is a task as its harness starts from it: the task, its organization's slug, which the harness's calls name, and
// its agent's standing instructions.
type Work struct {
	Task              Task
	OrgSlug           string
	AgentInstructions string
}

// Work answers the task the request's task token works, for the harness to start from; auth.ErrForbidden for any
// other credential.
func (s *Service) Work(ctx context.Context, v Viewer) (Work, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.Work")
	defer span.End()
	id, ok := working(ctx, v)
	if !ok {
		return Work{}, failed(span, "finding the task", fmt.Errorf("%w: only a task token works a task", auth.ErrForbidden))
	}
	w, err := s.store.TaskByID(ctx, id)
	if err != nil {
		return Work{}, failed(span, "finding the task", err)
	}
	span.SetAttributes(attribute.String("cobranch.org.id", w.Task.OrgID.String()), attribute.Int64("cobranch.task.number", w.Task.Number))
	return w, nil
}

// readable finds one of the organization's tasks whose transcript v reads: the task's own token reads it; others take
// what reading the task takes, and, for a task that names no repository, being its assigner or org.principals.manage.
func (s *Service) readable(ctx context.Context, v Viewer, orgID uuid.UUID, number int64) (Task, error) {
	if id, ok := working(ctx, v); ok {
		t, err := s.store.Task(ctx, orgID, number)
		if err == nil && t.ID == id {
			return t, nil
		}
	}
	t, err := s.Task(ctx, v, orgID, number)
	if err != nil || t.RepoID != uuid.Nil || (t.AssignedBy != nil && t.AssignedBy.ID == v.ID) {
		return t, err
	}
	if _, err := s.require(ctx, v, orgID, orgs.PermPrincipalsManage); err != nil {
		return Task{}, fmt.Errorf("reading another's transcript: %w", err)
	}
	return t, nil
}

// Transcript lists at most limit, 1 to MaxEntriesPage, of the entries of one of the organization's tasks numbered
// above after, in order. The task's own token reads it, and so do its assigner, org.principals.manage, and whoever
// reads the repository it names.
func (s *Service) Transcript(ctx context.Context, v Viewer, orgID uuid.UUID, number int64, after, limit int) ([]Entry, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.Transcript", onOrg(orgID, attribute.Int64("cobranch.task.number", number)))
	defer span.End()
	if limit < 1 || limit > MaxEntriesPage {
		return nil, failed(span, "reading the transcript", invalid("limit", "limit.invalid", fmt.Sprintf("a page holds 1 to %d entries", MaxEntriesPage)))
	}
	t, err := s.readable(ctx, v, orgID, number)
	if err != nil {
		return nil, failed(span, "reading the transcript", err)
	}
	entries, err := s.store.Entries(ctx, t.ID, max(after, 0), limit)
	return entries, failed(span, "reading the transcript", err)
}

// Append records entry e of one of the organization's tasks, the next of its transcript, and answers it and whether
// it recorded it: an entry already numbered e.Seq answers that one unchanged, so a harness that repeats an append
// after a failure records it once. Only the task's own token appends, while the task hasn't ended; a state entry moves
// the task too, in the same transaction.
func (s *Service) Append(ctx context.Context, v Viewer, orgID uuid.UUID, number int64, e Entry) (Entry, bool, error) {
	ctx, span := tracer.Start(ctx, "agents.Service.Append", onOrg(orgID, attribute.Int64("cobranch.task.number", number),
		attribute.Int("cobranch.transcript.seq", e.Seq), attribute.String("cobranch.transcript.kind", string(e.Kind))))
	defer span.End()
	id, ok := working(ctx, v)
	if !ok {
		return Entry{}, false, failed(span, "appending to the transcript", fmt.Errorf("%w: only the task's own token appends to its transcript", auth.ErrForbidden))
	}
	t, err := s.store.Task(ctx, orgID, number)
	switch {
	case err != nil:
		return Entry{}, false, failed(span, "finding the task", err)
	case t.ID != id:
		return Entry{}, false, failed(span, "appending to the transcript", fmt.Errorf("%w: a token appends to its own task's transcript", auth.ErrForbidden))
	}
	change, err := ValidateEntry(e)
	if err != nil {
		return Entry{}, false, failed(span, "appending to the transcript", err)
	}
	out, created, err := s.store.Append(ctx, v.ID, t.ID, e, change)
	return out, created, failed(span, "appending to the transcript", err)
}
