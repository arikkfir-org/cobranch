package harness_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/harness"
)

// askCases are TestChat's cases of a task that asks in its thread and waits for the answer.
func askCases() []chatCase {
	ours, theirs, question := uuid.MustParse("0190f1b2-0000-7000-8000-0000000000aa"), uuid.MustParse("0190f1b2-0000-7000-8000-0000000000bb"),
		uuid.MustParse("0190f1b2-0000-7000-8000-0000000000cc")
	tidy := harness.Chat{Repo: "website", Branch: "tidy"}
	at := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	olivia := &harness.ChatMessage{ID: uuid.New(), Author: "Olivia", At: at, Body: "Tidy."}
	asked := func(key uuid.UUID) []agents.Entry {
		e := entry(nil, agents.EntryInput, `{"text":"# Sweep"}`)
		e = entry(e, agents.EntryTurn, `{"calls":[{"id":"a","name":"ask","arguments":{"question":"Main or tidy?"},"key":"`+key.String()+`"}]}`)
		return entry(e, agents.EntryState, `{"state":"waiting"}`)
	}
	answered := kinds("input", "turn", "state", "state", "result", "turn", "state")
	return []chatCase{
		{name: "a question that starts the task's thread, and its answer", branch: "tidy",
			threads: map[uuid.UUID]*thread{theirs: {chat: tidy}},
			turns:   []harness.Turn{calling(call("a", "ask", `{"question":"Main or tidy?"}`)), done},
			waits: []wait{{}, {thread: theirs, message: &harness.ChatMessage{ID: uuid.New(), Author: "Max", Body: "Elsewhere."}},
				{message: &harness.ChatMessage{ID: uuid.New(), Author: "Scout", Task: 7, Body: "Still here."}}, {message: olivia}},
			check: askedAndAnswered},
		{name: "a question asked again in the task's next pod, once answered", branch: "tidy", state: agents.TaskWaiting, thread: ours,
			threads: map[uuid.UUID]*thread{ours: {chat: tidy, messages: []harness.ChatMessage{
				{ID: uuid.New(), Author: "Olivia", Body: "Please sweep."}, {ID: question, Author: "Scout", Task: 7, Body: "Main or tidy?"},
				{ID: uuid.New(), Deleted: true}, {ID: uuid.New(), Author: "Reviewer", Task: 9, Body: "Tidy has the dust."}, *olivia}}},
			entries: asked(question), turns: []harness.Turn{done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				if got := c.kinds(); !slices.Equal(got, answered) || c.waited != 0 || len(c.threads[ours].messages) != 5 {
					t.Fatalf("the transcript's entries are %v, after %d waits, and the thread holds %+v", got, c.waited, c.threads[ours].messages)
				}
				checkStates(t, c, "waiting", "running")
				body := string(c.entries[4].Body)
				containsAll(t, body, "From Reviewer (an agent, on task #9)", "Tidy has the dust.", "Tidy.")
				if strings.Contains(body, "Please sweep.") || strings.Contains(body, "(deleted)") {
					t.Errorf("ask answered %s, with what isn't an answer", body)
				}
			}},
		{name: "a question that started the task's thread, asked again", branch: "tidy", state: agents.TaskWaiting, thread: ours,
			threads: map[uuid.UUID]*thread{ours: {chat: tidy, messages: []harness.ChatMessage{
				{ID: uuid.New(), Author: "Scout", Task: 7, Body: "Main or tidy?"}, *olivia}}},
			entries: asked(ours), turns: []harness.Turn{done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				if body := string(c.entries[4].Body); !strings.Contains(body, "Tidy.") || strings.Contains(body, "Main or tidy?") {
					t.Errorf("ask answered %s", body)
				}
			}},
	}
}

// askFailureCases are TestChat's cases of a question that can't be asked, or whose answer doesn't come.
func askFailureCases() []chatCase {
	answered := kinds("input", "turn", "state", "state", "result", "turn", "state")
	return []chatCase{
		{name: "an empty question", branch: "tidy", turns: []harness.Turn{calling(call("a", "ask", `{"question":" "}`)), done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				if got := c.kinds(); !slices.Equal(got, kinds("input", "turn", "result", "turn", "state")) {
					t.Errorf("the transcript's entries are %v", got)
				}
				resultAt(2, "ask failed: the message is empty", true)(t, c, nil, nil)
			}},
		{name: "a wait that fails", branch: "tidy", turns: []harness.Turn{calling(call("a", "ask", `{"question":"Main or tidy?"}`)), done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				if got := c.kinds(); !slices.Equal(got, answered) {
					t.Fatalf("the transcript's entries are %v", got)
				}
				checkStates(t, c, "waiting", "running")
				resultAt(4, "ask failed: waiting for the chat's updates: no one answered", true)(t, c, nil, nil)
			}},
		{name: "a pod that stops while its task waits", branch: "tidy", turns: []harness.Turn{calling(call("a", "ask", `{"question":"Main or tidy?"}`)), done},
			waits: []wait{{stop: true}}, wantErr: true,
			check: func(t *testing.T, c *cobranch, _ *model) {
				if got := c.kinds(); !slices.Equal(got, kinds("input", "turn", "state")) {
					t.Errorf("the transcript's entries are %v", got)
				}
			}},
	}
}

// askedAndAnswered checks that the task asked in a thread it started, waited, and read only the answer to it.
func askedAndAnswered(t *testing.T, c *cobranch, m *model) {
	if got := c.kinds(); !slices.Equal(got, kinds("input", "turn", "state", "state", "result", "turn", "state")) {
		t.Fatalf("the transcript's entries are %v", got)
	}
	checkStates(t, c, "waiting", "running")
	resultAt(4, "Answered, in the thread after your question:", false)(t, c, nil, nil)
	body := string(c.entries[4].Body)
	containsAll(t, body, "From Olivia at 2026-11-02 09:30 UTC", "Tidy.")
	if strings.Contains(body, "Elsewhere.") || strings.Contains(body, "Still here.") || strings.Contains(body, "Main or tidy?") {
		t.Errorf("ask answered %s", body)
	}
	if got := c.threads[c.started].messages; c.waited != 4 || len(got) != 3 || got[0].Body != "Main or tidy?" {
		t.Errorf("after %d waits, the task's thread holds %+v", c.waited, got)
	}
	containsAll(t, m.requests[0].System, "ask there with ask")
}

// checkStates checks that the transcript's third and fourth entries move the task to those states.
func checkStates(t *testing.T, c *cobranch, states ...string) {
	t.Helper()
	for i, state := range states {
		if body := string(c.entries[2+i].Body); body != `{"state":"`+state+`"}` {
			t.Errorf("entry %d = %s, want the task %s", 3+i, body, state)
		}
	}
}
