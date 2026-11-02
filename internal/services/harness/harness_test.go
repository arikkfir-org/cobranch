package harness_test

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

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
	// threads are the chats' threads by ID, each with the chat it is of and its messages.
	threads map[uuid.UUID]*thread
	// postErr fails every start and post.
	postErr error
	// waits are the chat's updates, one per wait for them, which fails once they run out; started is the thread the
	// harness started, and cancel stops the harness's context.
	waits   []wait
	waited  int
	started uuid.UUID
	cancel  context.CancelFunc
}

// wait is what a wait for the chat's updates sees: nothing, a message someone posts to a thread, the task's own when
// thread is uuid.Nil, or its pod stopping.
type wait struct {
	thread  uuid.UUID
	message *harness.ChatMessage
	stop    bool
}

func (c *cobranch) ChatCursor(_ context.Context, org string, _ harness.Chat) (string, error) {
	if org != c.work.OrgSlug {
		return "", errors.New("another organization")
	}
	return "cursor-" + strconv.Itoa(c.waited), nil
}

func (c *cobranch) ChatUpdates(ctx context.Context, _ string, chat harness.Chat, after string) ([]uuid.UUID, string, error) {
	if after != "cursor-"+strconv.Itoa(c.waited) {
		return nil, "", fmt.Errorf("waited after %s, %d waits in", after, c.waited)
	}
	if c.waited >= len(c.waits) {
		return nil, "", errors.New("no one answered")
	}
	w := c.waits[c.waited]
	c.waited++
	next := "cursor-" + strconv.Itoa(c.waited)
	switch {
	case w.stop:
		c.cancel()
		return nil, "", fmt.Errorf("waiting: %w", ctx.Err())
	case w.message == nil:
		return nil, next, nil
	}
	id := w.thread
	if id == uuid.Nil {
		id = cmp.Or(c.work.Task.ThreadID, c.started)
	}
	t, ok := c.threads[id]
	if !ok || t.chat != chat {
		return nil, "", errors.New("no such thread")
	}
	t.messages = append(t.messages, *w.message)
	return []uuid.UUID{id}, next, nil
}

type thread struct {
	chat     harness.Chat
	messages []harness.ChatMessage
}

func (c *cobranch) Thread(_ context.Context, org string, chat harness.Chat, id uuid.UUID) ([]harness.ChatMessage, error) {
	t, ok := c.threads[id]
	if org != c.work.OrgSlug || !ok || t.chat != chat {
		return nil, errors.New("no such thread")
	}
	return slices.Clone(t.messages), nil
}

func (c *cobranch) StartThread(_ context.Context, org string, chat harness.Chat, id uuid.UUID, body string) error {
	if c.postErr != nil {
		return c.postErr
	}
	if org != c.work.OrgSlug {
		return errors.New("another organization")
	}
	if c.threads == nil {
		c.threads = map[uuid.UUID]*thread{}
	}
	c.started = id
	if _, ok := c.threads[id]; !ok {
		c.threads[id] = &thread{chat: chat, messages: []harness.ChatMessage{c.mine(uuid.New(), body)}}
	}
	return nil
}

func (c *cobranch) Post(_ context.Context, org string, chat harness.Chat, threadID, id uuid.UUID, body string) error {
	if c.postErr != nil {
		return c.postErr
	}
	t, ok := c.threads[threadID]
	if org != c.work.OrgSlug || !ok || t.chat != chat {
		return errors.New("no such thread")
	}
	if !slices.ContainsFunc(t.messages, func(m harness.ChatMessage) bool { return m.ID == id }) {
		t.messages = append(t.messages, c.mine(id, body))
	}
	return nil
}

// mine is a message the task posts.
func (c *cobranch) mine(id uuid.UUID, body string) harness.ChatMessage {
	return harness.ChatMessage{ID: id, Author: c.work.Task.Agent.DisplayName, Task: c.work.Task.Number, At: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC),
		Body: body}
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

// named is a tool of its name that answers "ok".
type named string

func (n named) Spec() harness.Spec {
	return harness.Spec{Name: string(n), Description: "Says ok.", Parameters: json.RawMessage(`{"type":"object"}`)}
}

func (n named) Run(context.Context, harness.Call) (string, error) { return "ok", nil }

// kind is a task's Kind that gives it kit, or fails with err; left counts down the times its task is unfinished.
type kind struct {
	kit  harness.Kit
	err  error
	left int
}

func (k *kind) Prepare(context.Context, agents.Work) (harness.Kit, error) {
	if k.left > 0 {
		k.kit.Unfinished = func(context.Context) (string, error) {
			if k.left--; k.left >= 0 {
				return "submit the review first", nil
			}
			return "", nil
		}
	}
	return k.kit, k.err
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
	// kind, when set, prepares the task, of kind review, which started startedAgo.
	kind       *kind
	startedAgo time.Duration
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
	tt = append(tt, kindCases()...)
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

// kindCases are the cases of tasks of a kind the harness handles.
func kindCases() []harnessCase {
	asked := func(n int) func(t *testing.T, _ *cobranch, m *model, _ *echo) {
		return func(t *testing.T, _ *cobranch, m *model, _ *echo) {
			if len(m.requests) != n {
				t.Errorf("asked the model %d times, want %d", len(m.requests), n)
			}
		}
	}
	return []harnessCase{
		{name: "a kind's tools and instructions", kind: &kind{kit: harness.Kit{Tools: []harness.Tool{named("stamp")}, Instructions: "Stamp it."}},
			turns: []harness.Turn{calling(call("a", "stamp", `{}`)), done}, want: kinds("input", "turn", "result", "turn", "state"),
			check: func(t *testing.T, c *cobranch, m *model, e *echo) {
				var names []string
				for _, s := range m.requests[0].Tools {
					names = append(names, s.Name)
				}
				if !slices.Equal(names, []string{"read_thread", "post_message", "ask", "echo", "stamp", "finish"}) ||
					!strings.HasSuffix(m.requests[0].System, "Be brief.\n\nStamp it.") {
					t.Errorf("the model is offered %v, with %q", names, m.requests[0].System)
				}
				resultAt(2, "ok", false)(t, c, m, e)
			}},
		{name: "a task its kind ends", kind: &kind{kit: harness.Kit{End: &agents.StateChange{State: agents.TaskSucceeded, Summary: "Nothing to stamp."}}},
			want: kinds("input", "state"), check: asked(0)},
		{name: "a success its kind refuses until the task is done", kind: &kind{left: 1}, turns: []harness.Turn{done, done},
			want: kinds("input", "turn", "result", "turn", "state"), check: resultAt(2, "finish can't end the task yet: submit the review first", true)},
		{name: "a failure its kind doesn't hold back", kind: &kind{left: 5},
			turns: []harness.Turn{calling(call("f", "finish", `{"outcome":"failed","reason":"No floor."}`))}, want: kinds("input", "turn", "state"),
			check: failedWith("No floor.")},
		{name: "a task past its kind's timeout", kind: &kind{kit: harness.Kit{Timeout: time.Minute}}, startedAgo: 2 * time.Minute,
			turns: []harness.Turn{done}, want: kinds("state"), check: failedWith("Timed out after 1m0s.")},
		{name: "a task its kind ended, resumed past its timeout",
			kind:       &kind{kit: harness.Kit{End: &agents.StateChange{State: agents.TaskSucceeded, Summary: "Reviewed it."}, Timeout: time.Minute}},
			startedAgo: 2 * time.Minute, want: kinds("input", "state"), check: func(t *testing.T, c *cobranch, m *model, e *echo) {
				if body := c.last(t); body["state"] != "succeeded" || c.work.Task.State != agents.TaskSucceeded {
					t.Errorf("the task ended with %v, want succeeded as its kind said", body)
				}
				asked(0)(t, c, m, e)
			}},
		{name: "a task within its kind's timeout", kind: &kind{kit: harness.Kit{Timeout: time.Hour}}, startedAgo: time.Minute,
			turns: []harness.Turn{done}, want: kinds("input", "turn", "state")},
		{name: "a kind that can't prepare the task", kind: &kind{err: errors.New("no diff")}, wantErr: true, check: asked(0)},
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
	h := harness.New(c, m, tc.maxTurns, e)
	if tc.kind != nil {
		c.work.Task.Kind, c.work.Task.StartedAt = "review", time.Now().Add(-tc.startedAgo)
		h.Handle("review", tc.kind)
	}
	err := h.Run(context.Background())
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
	if !slices.Equal(names, []string{"read_thread", "post_message", "ask", "echo", "finish"}) {
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
