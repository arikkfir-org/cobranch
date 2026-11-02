package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/orgs"
)

func TestAgentStoreTaskByID(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	task := f.assigned(t, "Look around", true, uuid.Nil)
	got, err := f.a.TaskByID(context.Background(), task.ID)
	if err != nil || got.Task.ID != task.ID || got.Task.Number != task.Number || got.Task.RepoSlug != "website" ||
		got.OrgSlug != f.acme.Slug || got.AgentInstructions != "Be brief." || got.AgentEmail != agents.Email(f.scout.ID) || got.IssueNumber != 0 {
		t.Errorf("TaskByID() = %+v, %v", got, err)
	}
	if _, err := f.a.TaskByID(context.Background(), uuid.New()); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("TaskByID(none) = %v, want %v", err, orgs.ErrNotFound)
	}
}

// transcriptStep is one append in TestAgentStoreTranscript, made after its task is set Running when running says so.
type transcriptStep struct {
	name        string
	running     bool
	entry       agents.Entry
	change      *agents.StateChange
	want        error
	wantCode    string
	wantCreated bool
	wantKind    agents.EntryKind
	wantState   agents.TaskState
}

func TestAgentStoreTranscript(t *testing.T) {
	t.Parallel()
	f := newAgentFixture(t)
	ctx := context.Background()
	task := f.assigned(t, "Look around", false, uuid.Nil)
	body := func(s string) json.RawMessage { return json.RawMessage(s) }
	waiting := &agents.StateChange{State: agents.TaskWaiting}
	done := &agents.StateChange{State: agents.TaskSucceeded, Summary: "Looked"}
	steps := []transcriptStep{
		{name: "the first entry", entry: agents.Entry{Seq: 1, Kind: agents.EntryInput, Body: body(`{"text": "Look"}`)},
			wantCreated: true, wantKind: agents.EntryInput, wantState: agents.TaskQueued},
		{name: "a repeat", entry: agents.Entry{Seq: 1, Kind: agents.EntryTurn, Body: body(`{}`)}, wantKind: agents.EntryInput, wantState: agents.TaskQueued},
		{name: "a gap", entry: agents.Entry{Seq: 3, Kind: agents.EntryTurn, Body: body(`{}`)}, wantCode: "transcript.seq_invalid"},
		{name: "waiting while queued", entry: agents.Entry{Seq: 2, Kind: agents.EntryState, Body: body(`{"state": "waiting"}`)}, change: waiting,
			want: agents.ErrTransition},
		{name: "waiting once running", running: true, entry: agents.Entry{Seq: 2, Kind: agents.EntryState, Body: body(`{"state": "waiting"}`)},
			change: waiting, wantCreated: true, wantKind: agents.EntryState, wantState: agents.TaskWaiting},
		{name: "succeeding", entry: agents.Entry{Seq: 3, Kind: agents.EntryState, Body: body(`{"state": "succeeded"}`)}, change: done,
			wantCreated: true, wantKind: agents.EntryState, wantState: agents.TaskSucceeded},
		{name: "a repeat once ended", entry: agents.Entry{Seq: 3, Kind: agents.EntryState, Body: body(`{}`)}, change: done,
			wantKind: agents.EntryState, wantState: agents.TaskSucceeded},
		{name: "an entry once ended", entry: agents.Entry{Seq: 4, Kind: agents.EntryTurn, Body: body(`{}`)}, want: agents.ErrEnded},
	}
	for _, tt := range steps {
		tt.run(t, f, task)
	}
	got, err := f.a.Task(ctx, f.acme.ID, task.Number)
	if err != nil || got.Summary != "Looked" || got.EndedAt.IsZero() {
		t.Errorf("Task() = %+v, %v, want it succeeded with its summary", got, err)
	}
	entries, err := f.a.Entries(ctx, task.ID, 1, 10)
	if err != nil || len(entries) != 2 || entries[0].Seq != 2 || entries[1].Seq != 3 {
		t.Fatalf("Entries(after 1) = %+v, %v", entries, err)
	}
	var state map[string]string
	if err := json.Unmarshal(entries[0].Body, &state); err != nil || state["state"] != "waiting" {
		t.Errorf("entry 2 = %s, %v", entries[0].Body, err)
	}
	if _, _, err := f.a.Append(ctx, f.scout.ID, uuid.New(), agents.Entry{Seq: 1, Kind: agents.EntryTurn, Body: body(`{}`)}, nil); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Append(no task) = %v, want %v", err, orgs.ErrNotFound)
	}
}

// run makes the step's append to task and checks what it answers and the state it leaves the task in.
func (tt transcriptStep) run(t *testing.T, f *agentFixture, task agents.Task) {
	t.Helper()
	ctx := context.Background()
	if tt.running {
		if _, err := f.pool.Exec(ctx, "UPDATE agent_tasks SET state = 'running', started_at = now() WHERE id = $1", task.ID); err != nil {
			t.Fatal(err)
		}
	}
	e, created, err := f.a.Append(ctx, f.scout.ID, task.ID, tt.entry, tt.change)
	if verr := (*orgs.ValidationError)(nil); tt.wantCode != "" {
		if !errors.As(err, &verr) || verr.Code != tt.wantCode {
			t.Fatalf("%s: Append() = %v, want %s", tt.name, err, tt.wantCode)
		}
		return
	}
	if !errors.Is(err, tt.want) {
		t.Fatalf("%s: Append() = %v, want %v", tt.name, err, tt.want)
	}
	if err != nil {
		return
	}
	if e.Seq != tt.entry.Seq || e.Kind != tt.wantKind || created != tt.wantCreated || e.CreatedAt.IsZero() {
		t.Errorf("%s: Append() = %+v, created %t", tt.name, e, created)
	}
	got, err := f.a.Task(ctx, f.acme.ID, task.Number)
	if err != nil || got.State != tt.wantState {
		t.Fatalf("%s: Task() = %s, %v, want %s", tt.name, got.State, err, tt.wantState)
	}
}
