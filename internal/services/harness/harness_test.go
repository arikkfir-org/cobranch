package harness_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/harness"
)

// cobranch is Cobranch's API over a transcript in memory, which takes what the real one takes.
type cobranch struct {
	work    agents.Work
	entries []agents.Entry
	// refuseAt is the entry whose append the token is refused at; 0 for none.
	refuseAt int
}

func (c *cobranch) Work(context.Context) (agents.Work, error) { return c.work, nil }

func (c *cobranch) Transcript(_ context.Context, org string, number int64) ([]agents.Entry, error) {
	if org != c.work.OrgSlug || number != c.work.Task.Number {
		return nil, errors.New("another task's transcript")
	}
	return slices.Clone(c.entries), nil
}

func (c *cobranch) Append(_ context.Context, org string, number int64, e agents.Entry) (agents.Entry, error) {
	switch {
	case org != c.work.OrgSlug || number != c.work.Task.Number:
		return agents.Entry{}, errors.New("another task's transcript")
	case e.Seq == c.refuseAt:
		return agents.Entry{}, fmt.Errorf("appending: %w", harness.ErrRefused)
	case e.Seq != len(c.entries)+1:
		return agents.Entry{}, fmt.Errorf("entry %d skips a number", e.Seq)
	}
	change, err := agents.ValidateEntry(e)
	if err != nil {
		return agents.Entry{}, fmt.Errorf("appending: %w", err)
	}
	if change != nil {
		c.work.Task.State = change.State
	}
	c.entries = append(c.entries, e)
	return e, nil
}

// last is the transcript's last entry's body, decoded into a map.
func (c *cobranch) last(t *testing.T) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(c.entries[len(c.entries)-1].Body, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// kinds lists the transcript's entries' kinds.
func (c *cobranch) kinds() []agents.EntryKind {
	out := make([]agents.EntryKind, 0, len(c.entries))
	for _, e := range c.entries {
		out = append(out, e.Kind)
	}
	return out
}

// model answers its turns in order, and the last one again once they run out.
type model struct {
	turns    []harness.Turn
	err      error
	requests []harness.Request
}

func (m *model) Next(_ context.Context, r harness.Request) (harness.Turn, error) {
	m.requests = append(m.requests, r)
	if m.err != nil {
		return harness.Turn{}, m.err
	}
	i := min(len(m.requests), len(m.turns)) - 1
	t := m.turns[i]
	t.Calls = slices.Clone(t.Calls)
	return t, nil
}

// echo answers its argument text, fails when fail is set, and remembers what it echoed.
type echo struct {
	ran []string
}

func (e *echo) Spec() harness.Spec {
	return harness.Spec{Name: "echo", Description: "Echoes text.", Parameters: json.RawMessage(`{"type":"object"}`)}
}

func (e *echo) Run(_ context.Context, c harness.Call) (string, error) {
	var args struct {
		Text string `json:"text"`
		Fail bool   `json:"fail"`
	}
	if err := json.Unmarshal(c.Arguments, &args); err != nil {
		return "", fmt.Errorf("reading its arguments: %w", err)
	}
	if _, err := uuid.Parse(c.Key); err != nil {
		return "", fmt.Errorf("the call has no key: %w", err)
	}
	e.ran = append(e.ran, args.Text)
	if args.Fail {
		return "half done", errors.New("it broke")
	}
	return args.Text, nil
}

// call is a call of tool name with the arguments args, a JSON object.
func call(id, name, args string) harness.Call {
	return harness.Call{ID: id, Name: name, Arguments: json.RawMessage(args)}
}

// calling is a turn that makes calls.
func calling(calls ...harness.Call) harness.Turn {
	return harness.Turn{Calls: calls, Stop: "tool_use"}
}

// done is a turn that finishes the task as succeeded.
var done = calling(call("f", "finish", `{"outcome":"succeeded","summary":"Swept."}`))

// entry is an entry of the transcript, numbered after the entries before it.
func entry(before []agents.Entry, kind agents.EntryKind, body string) []agents.Entry {
	return append(before, agents.Entry{Seq: len(before) + 1, Kind: kind, Body: json.RawMessage(body)})
}

// harnessCase is a task the harness works, from a transcript, with its model's turns.
type harnessCase struct {
	name     string
	state    agents.TaskState
	entries  []agents.Entry
	turns    []harness.Turn
	modelErr error
	maxTurns int
	refuseAt int
	wantErr  bool
	want     []agents.EntryKind
	check    func(t *testing.T, c *cobranch, m *model, e *echo)
}

func TestRun(t *testing.T) {
	begun := entry(nil, agents.EntryInput, `{"text":"# Sweep\n\nSweep the floor."}`)
	tt := []harnessCase{
		{name: "a new task", turns: []harness.Turn{calling(call("a", "echo", `{"text":"hi"}`)), done},
			want: kinds("input", "turn", "result", "turn", "state"), check: firstTask},
		{name: "a restarted one runs only the calls without results",
			entries: entry(entry(begun, agents.EntryTurn, `{"calls":[{"id":"a","name":"echo","arguments":{"text":"one"},"key":"0190f1b2-0000-7000-8000-000000000001"},`+
				`{"id":"b","name":"echo","arguments":{"text":"two"},"key":"0190f1b2-0000-7000-8000-000000000002"}]}`), agents.EntryResult, `{"callId":"a","name":"echo","text":"one"}`),
			turns: []harness.Turn{done}, want: kinds("input", "turn", "result", "result", "turn", "state"),
			check: func(t *testing.T, _ *cobranch, m *model, e *echo) {
				if !slices.Equal(e.ran, []string{"two"}) {
					t.Errorf("ran %v, want only two", e.ran)
				}
				results := m.requests[0].Messages[2].Results
				if len(m.requests[0].Messages) != 3 || len(results) != 2 || results[0].Text != "one" || results[1].Text != "two" {
					t.Errorf("the model sees %+v, want both results in one message", m.requests[0].Messages)
				}
			}},
		{name: "an ended task", state: agents.TaskSucceeded, entries: begun, want: kinds("input"),
			check: func(t *testing.T, _ *cobranch, m *model, _ *echo) {
				if len(m.requests) != 0 {
					t.Errorf("asked the model %d times", len(m.requests))
				}
			}},
		{name: "the most turns", maxTurns: 2, turns: []harness.Turn{calling(call("a", "echo", `{"text":"again"}`))},
			want: kinds("input", "turn", "result", "turn", "result", "state"), check: failedWith("The model took 2 turns without finishing")},
		{name: "a provider's refusal", modelErr: fmt.Errorf("no such model: %w", harness.ErrModelRefused),
			want: kinds("input", "state"), check: failedWith("Asking the model failed: no such model: the model's provider refused the request")},
		{name: "a provider's failure", modelErr: errors.New("overloaded"), wantErr: true, want: kinds("input")},
		{name: "a turn without calls", turns: []harness.Turn{{Text: "Thinking.", Stop: "end_turn"}, done},
			want: kinds("input", "turn", "input", "turn", "state"), check: func(t *testing.T, _ *cobranch, m *model, _ *echo) {
				if got := m.requests[1].Messages[2]; got.Role != harness.RoleUser || !strings.Contains(got.Text, "call finish") {
					t.Errorf("the model is nudged with %+v", got)
				}
			}},
		{name: "a refused token", refuseAt: 2, turns: []harness.Turn{done}, want: kinds("input")},
	}
	tt = append(tt, callCases()...)
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runCase(t, tc)
		})
	}
}

// callCases are the cases of the calls the model makes.
func callCases() []harnessCase {
	big := strings.Repeat("é", harness.MaxOutput)
	return []harnessCase{
		{name: "an unknown tool", turns: []harness.Turn{calling(call("a", "sweep", `{}`)), done},
			want: kinds("input", "turn", "result", "turn", "state"), check: resultAt(2, `there is no tool called "sweep"`, true)},
		{name: "a tool's failure", turns: []harness.Turn{calling(call("a", "echo", `{"text":"x","fail":true}`)), done},
			want: kinds("input", "turn", "result", "turn", "state"), check: resultAt(2, "echo failed: it broke\n\nhalf done", true)},
		{name: "arguments that aren't JSON", turns: []harness.Turn{calling(call("a", "echo", `{"text":`)), done},
			want: kinds("input", "turn", "result", "turn", "state"), check: resultAt(2, "echo failed: reading its arguments: json: cannot unmarshal string", true)},
		{name: "a finish the task can't end with", turns: []harness.Turn{calling(call("f", "finish", `{"outcome":"succeeded"}`)), done},
			want: kinds("input", "turn", "result", "turn", "state"), check: resultAt(2, "finish can't end the task: a summary is 1 to 20000 characters", true)},
		{name: "a finish that fails the task", turns: []harness.Turn{calling(call("f", "finish", `{"outcome":"failed","reason":"No floor."}`))},
			want: kinds("input", "turn", "state"), check: failedWith("No floor.")},
		{name: "finish runs after the turn's other calls",
			turns: []harness.Turn{calling(call("f", "finish", `{"outcome":"succeeded","summary":"Swept."}`), call("a", "echo", `{"text":"last"}`))},
			want:  kinds("input", "turn", "result", "state"), check: resultAt(2, "last", false)},
		{name: "a large output", turns: []harness.Turn{calling(call("a", "echo", `{"text":"`+big+`"}`)), done},
			want: kinds("input", "turn", "result", "turn", "state"), check: func(t *testing.T, c *cobranch, m *model, _ *echo) {
				var r struct {
					Text string `json:"text"`
					Cut  bool   `json:"cut"`
					Size int    `json:"size"`
				}
				if err := json.Unmarshal(c.entries[2].Body, &r); err != nil || !r.Cut || r.Size != len(big) || len(r.Text) > harness.MaxOutput || !strings.HasPrefix(big, r.Text) {
					t.Errorf("result = cut %v, size %d, %d bytes, %v", r.Cut, r.Size, len(r.Text), err)
				}
				if seen := m.requests[1].Messages[2].Results[0].Text; !strings.HasSuffix(seen, "[Cut: the output was 131072 bytes, and this is its first 65536.]") {
					t.Errorf("the model sees %q", seen[len(seen)-80:])
				}
			}},
		{name: "a turn too large to record", turns: []harness.Turn{calling(call("a", "echo", `{"text":"`+big+big+big+big+`"}`)), done},
			want: kinds("input", "turn", "result", "turn", "state"), check: func(t *testing.T, c *cobranch, _ *model, e *echo) {
				if len(e.ran) != 0 || !strings.Contains(string(c.entries[1].Body), `"arguments":{},`) {
					t.Errorf("ran %d, recorded %.200s", len(e.ran), c.entries[1].Body)
				}
				resultAt(2, "the turn was too large to record whole", true)(t, c, nil, e)
			}},
	}
}

// runCase works tc's task with an echo tool, and checks the transcript it leaves.
func runCase(t *testing.T, tc harnessCase) {
	state := tc.state
	if state == "" {
		state = agents.TaskRunning
	}
	c := &cobranch{refuseAt: tc.refuseAt, entries: slices.Clone(tc.entries), work: agents.Work{OrgSlug: "acme", AgentInstructions: "Be brief.",
		Task: agents.Task{Number: 7, Title: "Sweep", Instructions: "Sweep the floor.", State: state, Agent: agents.Principal{DisplayName: "Scout"},
			AssignedBy: &agents.Principal{ID: uuid.New(), DisplayName: "Olivia"}, RepoSlug: "website", Branch: "tidy"}}}
	m, e := &model{turns: tc.turns, err: tc.modelErr}, &echo{}
	err := harness.New(c, m, tc.maxTurns, e).Run(context.Background())
	if (err != nil) != tc.wantErr {
		t.Fatalf("Run() = %v, want an error: %v", err, tc.wantErr)
	}
	if got := c.kinds(); !slices.Equal(got, tc.want) {
		t.Errorf("the transcript's entries are %v, want %v", got, tc.want)
	}
	if tc.check != nil {
		tc.check(t, c, m, e)
	}
}

// firstTask checks a new task's transcript and what its model was asked.
func firstTask(t *testing.T, c *cobranch, m *model, e *echo) {
	t.Helper()
	if body := c.last(t); body["state"] != "succeeded" || body["summary"] != "Swept." || !slices.Equal(e.ran, []string{"hi"}) {
		t.Errorf("the task ended with %v, having echoed %v", body, e.ran)
	}
	first := m.requests[0]
	for _, want := range []string{"You are Scout, an agent of the organization acme on Cobranch, working on its task #7, which Olivia assigned to you.",
		"branch tidy of repository website", "call finish", "Be brief."} {
		if !strings.Contains(first.System, want) {
			t.Errorf("the system prompt %q doesn't say %q", first.System, want)
		}
	}
	if len(first.Messages) != 1 || first.Messages[0].Role != harness.RoleUser || first.Messages[0].Text != "# Sweep\n\nSweep the floor." {
		t.Errorf("the model is first asked %+v", first.Messages)
	}
	names := []string{}
	for _, s := range first.Tools {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"echo", "finish"}) {
		t.Errorf("the model is offered %v", names)
	}
	second := m.requests[1].Messages
	if len(second) != 3 || second[1].Role != harness.RoleAssistant || second[1].Calls[0].Name != "echo" || second[2].Results[0] != (harness.Result{CallID: "a", Name: "echo", Text: "hi"}) {
		t.Errorf("the model is then asked %+v", second)
	}
}

// failedWith checks that the task failed for a reason that starts with reason.
func failedWith(reason string) func(t *testing.T, c *cobranch, _ *model, _ *echo) {
	return func(t *testing.T, c *cobranch, _ *model, _ *echo) {
		t.Helper()
		if body := c.last(t); body["state"] != "failed" || !strings.HasPrefix(fmt.Sprint(body["reason"]), reason) || c.work.Task.State != agents.TaskFailed {
			t.Errorf("the task ended with %v, want failed: %s", body, reason)
		}
	}
}

// resultAt checks that the entry at i is a result whose text starts with text, and is an error's when failed.
func resultAt(i int, text string, failed bool) func(t *testing.T, c *cobranch, _ *model, _ *echo) {
	return func(t *testing.T, c *cobranch, _ *model, _ *echo) {
		t.Helper()
		var r struct {
			Text  string `json:"text"`
			Error bool   `json:"error"`
		}
		if err := json.Unmarshal(c.entries[i].Body, &r); err != nil || !strings.HasPrefix(r.Text, text) || r.Error != failed {
			t.Errorf("entry %d = %s, want a result %q, failed: %v", i+1, c.entries[i].Body, text, failed)
		}
	}
}

// kinds lists entry kinds by their names.
func kinds(names ...string) []agents.EntryKind {
	out := make([]agents.EntryKind, 0, len(names))
	for _, n := range names {
		out = append(out, agents.EntryKind(n))
	}
	return out
}
