package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/jobs"
)

// run is r as the API shows it.
func run(r code.Run) (*Run, error) {
	def, err := job(r.Definition)
	if err != nil {
		return nil, fmt.Errorf("showing run #%d: %w", r.Number, err)
	}
	out := &Run{ID: r.ID.String(), Number: int(r.Number), Job: commitPrincipal(&r.Job), Sha: r.SHA, Ref: r.Ref,
		Trigger: trigger(r.Trigger), Definition: def, State: RunState(strings.ToUpper(string(r.State))), Reason: r.Reason,
		StartedBy: commitPrincipal(r.StartedBy), QueuedAt: r.QueuedAt.UTC(), StartedAt: utc(r.StartedAt),
		EndedAt: utc(r.EndedAt), Version: int(r.Version)}
	if r.CauseRun != 0 {
		cause := int(r.CauseRun)
		out.CauseRun = &cause
	}
	return out, nil
}

// trigger is e as the API shows it.
func trigger(e jobs.Event) *RunTrigger {
	return &RunTrigger{Kind: string(e.Kind), Sha: e.SHA, Ref: e.Ref, Name: e.Name, State: e.State, Actor: idOf(e.ActorID),
		Push: idOf(e.PushID)}
}

// job is j as the API shows it: its triggers, pod spec and secrets as JSON objects.
func job(j jobs.Job) (*Job, error) {
	out := &Job{Name: j.Name, Kind: string(j.Kind()), Permissions: j.Permissions, Checkout: j.Checkout,
		Script: nonEmpty(j.Script), Image: nonEmpty(j.Image)}
	if err := object(j.On, &out.On); err != nil {
		return nil, fmt.Errorf("showing job %s's triggers: %w", j.Name, err)
	}
	if j.Pod != nil {
		if err := json.Unmarshal(j.Pod, &out.Pod); err != nil {
			return nil, fmt.Errorf("showing job %s's pod: %w", j.Name, err)
		}
	}
	if j.Secrets != nil {
		out.Secrets = make(map[string]any, len(j.Secrets))
		for env, id := range j.Secrets {
			out.Secrets[env] = id.String()
		}
	}
	if j.Webhook != nil {
		out.WebhookURL = &j.Webhook.URL
	}
	if j.Timeout != 0 {
		timeout := time.Duration(j.Timeout).String()
		out.Timeout = &timeout
	}
	return out, nil
}

// object copies in to the JSON object out through JSON.
func object(in any, out *map[string]any) error {
	encoded, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encoding: %w", err)
	}
	if err := json.Unmarshal(encoded, out); err != nil {
		return fmt.Errorf("decoding: %w", err)
	}
	return nil
}

// runsCursor is the run number a page of runs starts before, as after names it: 0 for the newest, and a
// BAD_USER_INPUT error for anything but a number from 1.
func runsCursor(ctx context.Context, after *string) (int64, error) {
	if after == nil {
		return 0, nil
	}
	before, err := strconv.ParseInt(*after, 10, 64)
	if err != nil || before < 1 {
		return 0, refuse(ctx, badUserInput, "cursor.invalid", "start from the cursor the previous page's nextCursor names")
	}
	return before, nil
}

// runPage is page as the API shows it.
func runPage(page code.RunPage) (*RunPage, error) {
	out := &RunPage{Runs: make([]*Run, 0, len(page.Runs))}
	for _, r := range page.Runs {
		shown, err := run(r)
		if err != nil {
			return nil, err
		}
		out.Runs = append(out.Runs, shown)
	}
	if page.Next != 0 {
		next := strconv.FormatInt(page.Next, 10)
		out.NextCursor = &next
	}
	return out, nil
}

// idOf is id as the API shows it, or nil for none.
func idOf(id uuid.UUID) *string {
	if id == uuid.Nil {
		return nil
	}
	s := id.String()
	return &s
}

// nonEmpty is s, or nil when it is empty.
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// utc is t in UTC, or nil without one.
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	at := t.UTC()
	return &at
}
