package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/orgs"
)

// finishName is the tool that ends a task, which the harness runs itself.
const finishName = "finish"

// finishSpec describes finish to the model.
var finishSpec = Spec{
	Name: finishName,
	Description: "Ends the task. Call it once the task is done, with a summary of what you did, or once you find you can't " +
		"do it, with the reason. Nothing runs after it, so call it alone, after every other call has its result.",
	Parameters: json.RawMessage(`{"type":"object","properties":{` +
		`"outcome":{"type":"string","enum":["succeeded","failed"],"description":"succeeded when the task is done; failed when you can't do it"},` +
		`"summary":{"type":"string","description":"For succeeded: what you did, in Markdown, for the person who assigned the task"},` +
		`"reason":{"type":"string","description":"For failed: why you can't do it, in a sentence or two"}},` +
		`"required":["outcome"]}`),
}

// errDropped is the failure of a call whose arguments a turn too large to record left out.
var errDropped = errors.New("the turn was too large to record whole, so this call's arguments were left out and it didn't run: " +
	"make smaller calls, such as writing a large file in parts")

// specs describes the tools to the model, finish last.
func (r *run) specs() []Spec {
	out := make([]Spec, 0, len(r.toolset)+1)
	for _, t := range r.toolset {
		out = append(out, t.Spec())
	}
	return append(out, finishSpec)
}

// call runs calls, the last turn's that have no result yet, recording each one's result: finish after the others, so
// that the task ends only once they have run.
func (r *run) call(ctx context.Context, calls []Call) error {
	var finishes []Call
	for _, c := range calls {
		if c.Name == finishName && !c.Dropped {
			finishes = append(finishes, c)
			continue
		}
		if err := r.runCall(ctx, c); err != nil {
			return err
		}
	}
	for _, c := range finishes {
		if ended, err := r.finish(ctx, c); err != nil || ended {
			return err
		}
	}
	return nil
}

// runCall runs a tool's call and records its result, a failure included. A call cut short by the pod stopping records
// nothing, so the next pod runs it again.
func (r *run) runCall(ctx context.Context, c Call) error {
	ctx, span := tracer.Start(ctx, "harness.runCall", trace.WithAttributes(attribute.String("cobranch.harness.tool", c.Name)))
	defer span.End()
	text, err := r.invoke(ctx, c)
	if ctx.Err() != nil {
		return failed(span, fmt.Errorf("running %s: %w", c.Name, ctx.Err()))
	}
	res := result{CallID: c.ID, Name: c.Name, Text: text}
	if err != nil {
		res.Error, res.Text = true, err.Error()
		if text != "" {
			res.Text += "\n\n" + text
		}
	}
	slog.InfoContext(ctx, "Ran a tool", "tool", c.Name, "failed", res.Error, "bytes", len(res.Text))
	return failed(span, r.append(ctx, agents.EntryResult, res.cut()))
}

// invoke runs the call with the tool it names.
func (r *run) invoke(ctx context.Context, c Call) (string, error) {
	if c.Dropped {
		return "", errDropped
	}
	for _, t := range r.toolset {
		if t.Spec().Name != c.Name {
			continue
		}
		out, err := t.Run(ctx, c)
		if err != nil {
			return out, fmt.Errorf("%s failed: %w", c.Name, err)
		}
		return out, nil
	}
	return "", fmt.Errorf("there is no tool called %q", c.Name)
}

// finish ends the task as the call says, and reports whether it did: a call the task can't end with, such as a success
// its kind says the task hasn't earned yet, records its failure instead, for the model to call it again.
func (r *run) finish(ctx context.Context, c Call) (bool, error) {
	change, err := finishing(c.Arguments)
	if err == nil && change.State == agents.TaskSucceeded && r.kit.Unfinished != nil {
		var left string
		if left, err = r.kit.Unfinished(ctx); err != nil {
			return false, fmt.Errorf("finding what the task has left to do: %w", err)
		}
		if left != "" {
			err = errors.New("finish can't end the task yet: " + left)
		}
	}
	if err != nil {
		return false, r.append(ctx, agents.EntryResult, result{CallID: c.ID, Name: c.Name, Text: err.Error(), Error: true}.cut())
	}
	slog.InfoContext(ctx, "The task finished", "state", change.State)
	return true, r.append(ctx, agents.EntryState, change)
}

// finishArgs are finish's arguments.
type finishArgs struct {
	Outcome agents.TaskState `json:"outcome"`
	Summary string           `json:"summary"`
	Reason  string           `json:"reason"`
}

// finishing reads finish's arguments into the state change they ask for, which Cobranch takes.
func finishing(args json.RawMessage) (agents.StateChange, error) {
	var a finishArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return agents.StateChange{}, fmt.Errorf("finish takes an object of outcome, summary and reason: %w", err)
	}
	if a.Outcome != agents.TaskSucceeded && a.Outcome != agents.TaskFailed {
		return agents.StateChange{}, errors.New("finish's outcome is succeeded or failed")
	}
	raw, err := json.Marshal(agents.StateChange{State: a.Outcome, Summary: a.Summary, Reason: a.Reason})
	if err != nil {
		return agents.StateChange{}, fmt.Errorf("writing the state change: %w", err)
	}
	change, err := agents.ValidateEntry(agents.Entry{Seq: 1, Kind: agents.EntryState, Body: raw})
	if verr := (*orgs.ValidationError)(nil); errors.As(err, &verr) {
		return agents.StateChange{}, errors.New("finish can't end the task: " + verr.Message)
	}
	if err != nil {
		return agents.StateChange{}, fmt.Errorf("finish can't end the task: %w", err)
	}
	return *change, nil
}
