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

// Run is one run of a repository's job as the schema shows it, with the repository it is of, from which its logs
// resolve.
type Run struct {
	ID         string           `json:"id"`
	Number     int              `json:"number"`
	Job        *CommitPrincipal `json:"job"`
	Sha        string           `json:"sha"`
	Ref        string           `json:"ref"`
	Trigger    *RunTrigger      `json:"trigger"`
	CauseRun   *int             `json:"causeRun,omitempty"`
	Attempt    int              `json:"attempt"`
	RerunOf    *int             `json:"rerunOf,omitempty"`
	Definition *Job             `json:"definition"`
	State      RunState         `json:"state"`
	Reason     string           `json:"reason"`
	StartedBy  *CommitPrincipal `json:"startedBy,omitempty"`
	QueuedAt   time.Time        `json:"queuedAt"`
	StartedAt  *time.Time       `json:"startedAt,omitempty"`
	EndedAt    *time.Time       `json:"endedAt,omitempty"`
	Version    int              `json:"version"`
	repoID     uuid.UUID
}

// run is r as the API shows it.
func run(r code.Run) (*Run, error) {
	def, err := job(r.Definition)
	if err != nil {
		return nil, fmt.Errorf("showing run #%d: %w", r.Number, err)
	}
	out := &Run{ID: r.ID.String(), Number: int(r.Number), Job: commitPrincipal(&r.Job), Sha: r.SHA, Ref: r.Ref,
		Trigger: trigger(r.Trigger), Definition: def, State: RunState(strings.ToUpper(string(r.State))), Reason: r.Reason,
		StartedBy: commitPrincipal(r.StartedBy), QueuedAt: r.QueuedAt.UTC(), StartedAt: utc(r.StartedAt),
		EndedAt: utc(r.EndedAt), Version: int(r.Version), repoID: r.RepoID}
	if r.CauseRun != 0 {
		cause := int(r.CauseRun)
		out.CauseRun = &cause
	}
	out.Attempt = int(max(r.Attempt, 1))
	if r.RerunOf != 0 {
		first := int(r.RerunOf)
		out.RerunOf = &first
	}
	return out, nil
}

// trigger is e as the API shows it.
func trigger(e jobs.Event) *RunTrigger {
	out := &RunTrigger{Kind: string(e.Kind), Sha: e.SHA, Ref: e.Ref, Name: e.Name, State: e.State, Actor: idOf(e.ActorID),
		Push: idOf(e.PushID)}
	if len(e.Data) > 0 {
		var data any
		if json.Unmarshal(e.Data, &data) == nil {
			out.Data = data
		}
	}
	if !e.Slot.IsZero() {
		slot := e.Slot.UTC()
		out.Slot = &slot
	}
	return out
}

// eventInput is the event postEvent posts: its data as JSON.
func eventInput(name string, branch *string, data any) (code.EventInput, error) {
	in := code.EventInput{Name: name}
	if branch != nil {
		in.Branch = *branch
	}
	if data != nil {
		encoded, err := json.Marshal(data)
		if err != nil {
			return in, fmt.Errorf("encoding the event's data: %w", err)
		}
		in.Data = encoded
	}
	return in, nil
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

// runLogs is logs as the API shows them, their bytes that aren't UTF-8 replaced.
func runLogs(logs []code.RunLog) []*RunLog {
	out := make([]*RunLog, 0, len(logs))
	for _, l := range logs {
		out = append(out, &RunLog{Container: l.Container, Text: strings.ToValidUTF8(string(l.Text), "\uFFFD"), Live: l.Live})
	}
	return out
}
