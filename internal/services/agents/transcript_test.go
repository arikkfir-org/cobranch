package agents_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// working is ctx under a task token of task's agent, assigned by the task's assigner.
func working(ctx context.Context, task agents.Task) context.Context {
	return auth.WithViewer(ctx, auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: task.Agent.ID, Kind: auth.KindAgent}},
		Credential: auth.Credential{Kind: auth.CredentialTaskToken, PrincipalID: task.Agent.ID,
			Task: auth.TaskGrant{ID: task.ID, OrgID: task.OrgID, AssignerID: task.AssignedBy.ID}}})
}

// agentViewer is the viewer task's agent is.
func agentViewer(task agents.Task) agents.Viewer {
	return agents.Viewer{Principal: auth.Principal{ID: task.Agent.ID, Kind: auth.KindAgent, DisplayName: task.Agent.DisplayName}}
}

func TestAppend(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	scout := f.agent(t, f.olive, "Scout")
	task, other := f.assign(t, f.mel, scout, "Tidy up", uuid.Nil), f.assign(t, f.mel, scout, "Sweep", uuid.Nil)
	mine, theirs := working(context.Background(), task), working(context.Background(), other)
	agent := agentViewer(task)
	tests := []struct {
		name string
		ctx  context.Context
		v    agents.Viewer
		// running moves the task to Running first, as the controller would.
		running     bool
		entry       agents.Entry
		want        error
		wantCreated bool
		wantState   agents.TaskState
	}{
		{name: "the first entry", ctx: mine, v: agent, entry: agents.Entry{Seq: 1, Kind: agents.EntryInput, Body: json.RawMessage(`{"text":"Tidy up"}`)},
			wantCreated: true, wantState: agents.TaskQueued},
		{name: "a repeat answers the entry there", ctx: mine, v: agent, entry: agents.Entry{Seq: 1, Kind: agents.EntryTurn, Body: json.RawMessage(`{}`)},
			wantState: agents.TaskQueued},
		{name: "a number past the next", ctx: mine, v: agent, entry: agents.Entry{Seq: 3, Kind: agents.EntryTurn, Body: json.RawMessage(`{}`)},
			want: invalid("transcript.seq_invalid")},
		{name: "no number", ctx: mine, v: agent, entry: agents.Entry{Kind: agents.EntryTurn, Body: json.RawMessage(`{}`)}, want: invalid("transcript.seq_invalid")},
		{name: "no kind", ctx: mine, v: agent, entry: agents.Entry{Seq: 2, Kind: "note", Body: json.RawMessage(`{}`)}, want: invalid("transcript.kind_invalid")},
		{name: "a body that isn't an object", ctx: mine, v: agent, entry: agents.Entry{Seq: 2, Kind: agents.EntryTurn, Body: json.RawMessage(`[1]`)},
			want: invalid("transcript.body_invalid")},
		{name: "a state no harness sets", ctx: mine, v: agent, entry: agents.Entry{Seq: 2, Kind: agents.EntryState, Body: json.RawMessage(`{"state":"cancelled"}`)},
			want: invalid("task.state_invalid")},
		{name: "waiting before the controller starts it", ctx: mine, v: agent, entry: agents.Entry{Seq: 2, Kind: agents.EntryState, Body: json.RawMessage(`{"state":"waiting"}`)},
			want: agents.ErrTransition},
		{name: "waiting once running", ctx: mine, v: agent, running: true, entry: agents.Entry{Seq: 2, Kind: agents.EntryState, Body: json.RawMessage(`{"state":"waiting"}`)},
			wantCreated: true, wantState: agents.TaskWaiting},
		{name: "succeeding without a summary", ctx: mine, v: agent, entry: agents.Entry{Seq: 3, Kind: agents.EntryState, Body: json.RawMessage(`{"state":"succeeded"}`)},
			want: invalid("task.summary_invalid")},
		{name: "another task's token", ctx: theirs, v: agent, entry: agents.Entry{Seq: 3, Kind: agents.EntryTurn, Body: json.RawMessage(`{}`)},
			want: auth.ErrForbidden},
		{name: "a person", ctx: context.Background(), v: f.mel, entry: agents.Entry{Seq: 3, Kind: agents.EntryTurn, Body: json.RawMessage(`{}`)},
			want: auth.ErrForbidden},
		{name: "succeeding", ctx: mine, v: agent, entry: agents.Entry{Seq: 3, Kind: agents.EntryState, Body: json.RawMessage(`{"state":"succeeded","summary":" Tidied "}`)},
			wantCreated: true, wantState: agents.TaskSucceeded},
		{name: "a repeat once it ended", ctx: mine, v: agent, entry: agents.Entry{Seq: 3, Kind: agents.EntryState, Body: json.RawMessage(`{"state":"succeeded","summary":"Tidied"}`)},
			wantState: agents.TaskSucceeded},
		{name: "a new entry once it ended", ctx: mine, v: agent, entry: agents.Entry{Seq: 4, Kind: agents.EntryTurn, Body: json.RawMessage(`{}`)},
			want: agents.ErrEnded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.running {
				f.store.SetState(f.org, task.Number, agents.TaskRunning)
			}
			e, created, err := f.service.Append(tt.ctx, tt.v, f.org, task.Number, tt.entry)
			if !is(err, tt.want) {
				t.Fatalf("Append() = %v, want %v", err, tt.want)
			}
			if err != nil {
				return
			}
			if created != tt.wantCreated || e.Seq != tt.entry.Seq {
				t.Errorf("Append() = %+v, created %t, want #%d, created %t", e, created, tt.entry.Seq, tt.wantCreated)
			}
			got, err := f.service.Task(context.Background(), f.mel, f.org, task.Number)
			if err != nil || got.State != tt.wantState {
				t.Fatalf("Task() = %s, %v, want %s", got.State, err, tt.wantState)
			}
			if got.State == agents.TaskSucceeded && (got.Summary != "Tidied" || got.EndedAt.IsZero()) {
				t.Errorf("Task() = %+v, want the summary and its end", got)
			}
		})
	}
}

func TestFailing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	task := f.assign(t, f.mel, f.agent(t, f.olive, "Scout"), "Tidy up", uuid.Nil)
	f.store.SetState(f.org, task.Number, agents.TaskRunning)
	ctx, agent := working(context.Background(), task), agentViewer(task)
	if _, _, err := f.service.Append(ctx, agent, f.org, task.Number, agents.Entry{Seq: 1, Kind: agents.EntryState,
		Body: json.RawMessage(`{"state":"failed"}`)}); !is(err, invalid("task.reason_invalid")) {
		t.Fatalf("Append(failed without a reason) = %v", err)
	}
	if _, _, err := f.service.Append(ctx, agent, f.org, task.Number, agents.Entry{Seq: 1, Kind: agents.EntryState,
		Body: json.RawMessage(`{"state":"failed","reason":"No model","summary":"dropped"}`)}); err != nil {
		t.Fatal(err)
	}
	got, err := f.service.Task(context.Background(), f.mel, f.org, task.Number)
	if err != nil || got.State != agents.TaskFailed || got.Reason != "No model" || got.Summary != "" {
		t.Errorf("Task() = %+v, %v, want failed for its reason", got, err)
	}
}

func TestTranscript(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	scout := f.agent(t, f.olive, "Scout")
	anywhere, website := f.assign(t, f.mel, scout, "Tidy up", uuid.Nil), f.assign(t, f.mel, scout, "Read", f.website)
	for _, task := range []agents.Task{anywhere, website} {
		for seq := 1; seq <= 3; seq++ {
			if _, _, err := f.service.Append(working(context.Background(), task), agentViewer(task), f.org, task.Number,
				agents.Entry{Seq: seq, Kind: agents.EntryTurn, Body: json.RawMessage(`{"text":"…"}`)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	tests := []struct {
		name     string
		ctx      context.Context
		v        agents.Viewer
		task     agents.Task
		after    int
		limit    int
		want     error
		wantSeqs []int
	}{
		{name: "the task's own token", ctx: working(context.Background(), anywhere), v: agentViewer(anywhere), task: anywhere, limit: 10, wantSeqs: []int{1, 2, 3}},
		{name: "its assigner, from an entry", v: f.mel, task: anywhere, after: 1, limit: 10, wantSeqs: []int{2, 3}},
		{name: "a page", v: f.mel, task: anywhere, limit: 2, wantSeqs: []int{1, 2}},
		{name: "a principals manager", v: f.stu, task: anywhere, limit: 10, wantSeqs: []int{1, 2, 3}},
		{name: "another member, of a task anywhere", v: f.max, task: anywhere, limit: 10, want: auth.ErrForbidden},
		{name: "a reader of its repository", v: f.max, task: website, limit: 10, wantSeqs: []int{1, 2, 3}},
		{name: "a guest who doesn't read it", v: f.gus, task: website, limit: 10, want: orgs.ErrNotFound},
		{name: "another task's token", ctx: working(context.Background(), website), v: agentViewer(website), task: anywhere, limit: 10,
			want: orgs.ErrNotFound},
		{name: "no member", v: f.nia, task: anywhere, limit: 10, want: orgs.ErrNotFound},
		{name: "a page too large", v: f.mel, task: anywhere, limit: agents.MaxEntriesPage + 1, want: invalid("limit.invalid")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			entries, err := f.service.Transcript(ctx, tt.v, f.org, tt.task.Number, tt.after, tt.limit)
			if !is(err, tt.want) {
				t.Fatalf("Transcript() = %v, want %v", err, tt.want)
			}
			var seqs []int
			for _, e := range entries {
				seqs = append(seqs, e.Seq)
			}
			if err == nil && !slices.Equal(seqs, tt.wantSeqs) {
				t.Errorf("Transcript() = %v, want %v", seqs, tt.wantSeqs)
			}
		})
	}
}

func TestWork(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.store.OrgSlugs[f.org] = "acme"
	task := f.assign(t, f.mel, f.agent(t, f.olive, "Scout"), "Tidy up", uuid.Nil)
	got, slug, err := f.service.Work(working(context.Background(), task), agentViewer(task))
	if err != nil || got.ID != task.ID || slug != "acme" {
		t.Errorf("Work() = %+v, %q, %v", got, slug, err)
	}
	if _, _, err := f.service.Work(context.Background(), f.mel); !is(err, auth.ErrForbidden) {
		t.Errorf("Work(a person) = %v, want %v", err, auth.ErrForbidden)
	}
}
