package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/agents"
)

// Harness works the task its token names until the task ends.
type Harness struct {
	cobranch Cobranch
	model    Model
	tools    []Tool
	maxTurns int
	checkout Checkout
	kinds    map[string]Kind
}

// New returns a Harness that works through c, asks m for its turns, offers it the tools and finish, and fails a task
// whose model takes maxTurns turns without finishing; DefaultMaxTurns for 0 or less.
func New(c Cobranch, m Model, maxTurns int, tools ...Tool) *Harness {
	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurns
	}
	return &Harness{cobranch: c, model: m, tools: tools, maxTurns: maxTurns, kinds: map[string]Kind{}}
}

// Handle has k prepare the tasks of kind, and answers h.
func (h *Harness) Handle(kind string, k Kind) *Harness {
	h.kinds[kind] = k
	return h
}

// WithCheckout has the Harness ready the task's workspace with c before it works the task, and answers it.
func (h *Harness) WithCheckout(c Checkout) *Harness {
	h.checkout = c
	return h
}

// Run works the task until it ends, from where its transcript stops. It returns nil once the task ends, or once
// Cobranch refuses its token, which it does once the task has ended or been cancelled; an error that a restart may get
// past otherwise.
func (h *Harness) Run(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "harness.Harness.Run")
	defer span.End()
	r, err := h.resume(ctx)
	if err == nil && r != nil {
		err = r.loop(ctx)
	}
	if errors.Is(err, ErrRefused) {
		slog.InfoContext(ctx, "Cobranch refused the task's token: the task ended, or the token expired", "error", err)
		return nil
	}
	return failed(span, err)
}

// resume reads the task and its transcript, readies its workspace, and answers the run that carries it on; nil when
// the task has ended.
func (h *Harness) resume(ctx context.Context) (*run, error) {
	w, err := h.cobranch.Work(ctx)
	if err != nil {
		return nil, fmt.Errorf("finding the task: %w", err)
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.String("cobranch.org.slug", w.OrgSlug), attribute.Int64("cobranch.task.number", w.Task.Number))
	if !w.Task.State.Active() {
		slog.InfoContext(ctx, "The task has ended", "state", w.Task.State)
		return nil, nil
	}
	entries, err := h.cobranch.Transcript(ctx, w.OrgSlug, w.Task.Number)
	if err != nil {
		return nil, fmt.Errorf("reading the transcript: %w", err)
	}
	var kit Kit
	if k, ok := h.kinds[w.Task.Kind]; ok {
		if kit, err = k.Prepare(ctx, w); err != nil {
			return nil, fmt.Errorf("preparing the %s task: %w", w.Task.Kind, err)
		}
	}
	slog.InfoContext(ctx, "Working the task", "entries", len(entries), "kind", w.Task.Kind)
	r := &run{Harness: h, work: w, entries: entries, kit: kit, thread: w.Task.ThreadID}
	r.toolset = slices.Concat(r.chatTools(), h.tools, kit.Tools)
	// A task its kind ended has no deadline: it ends as its kind said, however late it resumed.
	if start := w.Task.StartedAt; kit.Timeout > 0 && kit.End == nil {
		if start.IsZero() {
			start = time.Now()
		}
		r.deadline = start.Add(kit.Timeout)
	}
	return r, r.prepare(ctx)
}

// run is a task the harness works, and its transcript so far: the kit its kind gave it, the tools it offers, its
// chat's, the agent's and its kind's, and when it must have ended, zero for no deadline of its own.
type run struct {
	*Harness
	work     agents.Work
	entries  []agents.Entry
	kit      Kit
	toolset  []Tool
	deadline time.Time
	// thread is the thread of its chat the task talks in; uuid.Nil until it has one.
	thread uuid.UUID
	// clone is the clone of the task's repository that its workspace holds.
	clone Clone
}

// prepare readies the task's workspace, unless its kind ended it, and tells the model when a task under way got a
// fresh one, as it does when its pod starts again: what it didn't push is gone.
func (r *run) prepare(ctx context.Context) error {
	if r.checkout == nil || r.kit.End != nil {
		return nil
	}
	clone, err := r.checkout.Prepare(ctx, r.work)
	if err != nil {
		return fmt.Errorf("preparing the workspace: %w", err)
	}
	r.clone = clone
	if !clone.Fresh || len(r.entries) == 0 {
		return nil
	}
	return r.append(ctx, agents.EntryInput, input{Text: freshWorkspace, By: byHarness})
}

// loop takes the task's steps until it ends, and fails it once it runs past its deadline: a step the deadline cuts
// short fails it too.
func (r *run) loop(ctx context.Context) error {
	sctx := ctx
	if !r.deadline.IsZero() {
		var cancel context.CancelFunc
		sctx, cancel = context.WithDeadline(ctx, r.deadline)
		defer cancel()
	}
	for {
		if r.overdue() {
			return r.fail(ctx, "Timed out after "+r.kit.Timeout.String()+".")
		}
		done, err := r.step(sctx)
		if done || err != nil && !r.overdue() {
			return err
		}
	}
}

// overdue reports whether the task ran past its deadline.
func (r *run) overdue() bool {
	return !r.deadline.IsZero() && !time.Now().Before(r.deadline)
}

// step takes the task's next step, as its transcript says it is, and reports whether the task has ended: it begins
// the transcript, ends a task its kind ended, runs the calls of the last turn that have no result yet, nudges a model
// that ended its turn without one, fails a task that took its last turn, or asks the model for its next turn.
func (r *run) step(ctx context.Context) (bool, error) {
	s, err := read(r.entries)
	switch {
	case err != nil:
		return false, err
	case s.ended:
		return true, nil
	case len(r.entries) == 0:
		return false, r.begin(ctx)
	case r.kit.End != nil:
		slog.InfoContext(ctx, "The task's kind ended it", "state", r.kit.End.State)
		return true, r.append(ctx, agents.EntryState, *r.kit.End)
	case len(s.pending) > 0:
		return false, r.call(ctx, s.pending)
	case s.idle:
		return false, r.append(ctx, agents.EntryInput, input{Text: nudge, By: byHarness})
	case s.turns >= r.maxTurns:
		return true, r.fail(ctx, fmt.Sprintf("The model took %d turns without finishing, the most a task may take.", r.maxTurns))
	default:
		return false, r.ask(ctx, s)
	}
}

// begin records what the task begins with: its title and instructions.
func (r *run) begin(ctx context.Context) error {
	text := "# " + r.work.Task.Title
	if r.work.Task.Instructions != "" {
		text += "\n\n" + r.work.Task.Instructions
	}
	return r.append(ctx, agents.EntryInput, input{Text: text})
}

// ask asks the model for its next turn and records it, each call with a key of its own. A provider's refusal fails
// the task, since asking again would be refused again.
func (r *run) ask(ctx context.Context, s state) error {
	ctx, span := tracer.Start(ctx, "harness.ask", trace.WithAttributes(attribute.Int("cobranch.harness.turn", s.turns+1)))
	defer span.End()
	t, err := r.model.Next(ctx, r.request(s))
	if errors.Is(err, ErrModelRefused) {
		return failed(span, r.fail(ctx, "Asking the model failed: "+err.Error()))
	}
	if err != nil {
		return failed(span, fmt.Errorf("asking the model for its next turn: %w", err))
	}
	for i := range t.Calls {
		key, err := uuid.NewV7()
		if err != nil {
			return failed(span, fmt.Errorf("naming a call: %w", err))
		}
		t.Calls[i].Key = key.String()
	}
	slog.InfoContext(ctx, "The model took a turn", "turn", s.turns+1, "calls", len(t.Calls), "stop", t.Stop)
	return failed(span, r.append(ctx, agents.EntryTurn, recordable(t)))
}

// fail ends the task as failed, for reason.
func (r *run) fail(ctx context.Context, reason string) error {
	slog.InfoContext(ctx, "The task failed", "reason", reason)
	return r.append(ctx, agents.EntryState, agents.StateChange{State: agents.TaskFailed, Reason: cut(reason, agents.MaxReason)})
}

// append records the transcript's next entry, of kind with body.
func (r *run) append(ctx context.Context, kind agents.EntryKind, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("writing a %s entry: %w", kind, err)
	}
	seq := 1
	if n := len(r.entries); n > 0 {
		seq = r.entries[n-1].Seq + 1
	}
	e, err := r.cobranch.Append(ctx, r.work.OrgSlug, r.work.Task.Number, agents.Entry{Seq: seq, Kind: kind, Body: raw})
	if err != nil {
		return fmt.Errorf("appending entry %d, %s: %w", seq, kind, err)
	}
	r.entries = append(r.entries, e)
	return nil
}

// failed records err, if any, on span, and returns it.
func failed(span trace.Span, err error) error {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}
