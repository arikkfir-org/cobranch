package harness_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/harness"
)

// chatCase is a task that talks in a chat: where it works, the thread it has, its transcript and its model's turns.
type chatCase struct {
	name     string
	branch   string
	issue    int64
	thread   uuid.UUID
	threads  map[uuid.UUID]*thread
	entries  []agents.Entry
	turns    []harness.Turn
	postErr  error
	checkout *checkout
	wantErr  bool
	check    func(t *testing.T, c *cobranch, m *model)
}

// checkout is a harness.Checkout that clones, or fails, and remembers what it prepared.
type checkout struct {
	cloned   bool
	err      error
	prepared []agents.Work
}

func (k *checkout) Prepare(_ context.Context, w agents.Work) (bool, error) {
	k.prepared = append(k.prepared, w)
	return k.cloned, k.err
}

// keyed is a call with a key of its own, as the harness records it.
func keyed(c harness.Call, key uuid.UUID) harness.Call {
	c.Key = key.String()
	return c
}

func TestChat(t *testing.T) {
	ours, theirs := uuid.MustParse("0190f1b2-0000-7000-8000-0000000000aa"), uuid.MustParse("0190f1b2-0000-7000-8000-0000000000bb")
	tidy := harness.Chat{Repo: "website", Branch: "tidy"}
	begun := entry(nil, agents.EntryInput, `{"text":"# Sweep"}`)
	tt := []chatCase{
		{name: "the first post starts the task's thread, and the next replies there", branch: "tidy",
			turns: []harness.Turn{calling(call("a", "post_message", `{"body":"Starting."}`)), calling(call("b", "post_message", `{"body":"Swept."}`)),
				calling(call("c", "read_thread", `{}`)), done},
			check: func(t *testing.T, c *cobranch, m *model) {
				if len(c.threads) != 1 {
					t.Fatalf("started %d threads", len(c.threads))
				}
				for id, th := range c.threads {
					if th.chat != tidy || len(th.messages) != 2 || th.messages[0].Body != "Starting." || th.messages[1].Body != "Swept." {
						t.Errorf("thread %s = %+v", id, th)
					}
				}
				read := m.requests[3].Messages[6].Results[0].Text
				if !strings.Contains(read, "From you at 2026-11-02 09:00 UTC") || !strings.Contains(read, "Starting.") || !strings.Contains(read, "Swept.") {
					t.Errorf("read_thread answered %q", read)
				}
			}},
		{name: "a post starts no thread again after a restart", branch: "tidy", thread: ours,
			threads: map[uuid.UUID]*thread{ours: {chat: tidy, messages: []harness.ChatMessage{{ID: uuid.New(), Body: "Starting.", Task: 7}}}},
			entries: entry(begun, agents.EntryTurn, `{"calls":[{"id":"a","name":"post_message","arguments":{"body":"Starting."},"key":"`+ours.String()+`"}]}`),
			turns:   []harness.Turn{done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				if len(c.threads) != 1 || len(c.threads[ours].messages) != 1 {
					t.Errorf("the chat holds %+v", c.threads)
				}
				resultAt(2, "Posted, starting the task's thread.", false)(t, c, nil, nil)
			}},
		{name: "the task's thread", branch: "tidy", thread: theirs,
			threads: map[uuid.UUID]*thread{theirs: {chat: tidy, messages: []harness.ChatMessage{
				{ID: uuid.New(), Author: "Olivia", At: time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC), Body: "Please sweep."},
				{ID: uuid.New(), Author: "Reviewer", Task: 9, Body: "Looks dusty."},
				{ID: uuid.New(), Deleted: true}}}},
			turns: []harness.Turn{calling(keyed(call("a", "post_message", `{"body":"On it."}`), ours), call("b", "read_thread", `{}`)), done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				if got := c.threads[theirs].messages; len(got) != 4 || got[3].ID == ours {
					t.Errorf("the thread holds %+v", got)
				}
				resultAt(2, "Posted.", false)(t, c, nil, nil)
				resultAt(3, "From Olivia at 2026-11-02 08:00 UTC, message", false)(t, c, nil, nil)
				for _, want := range []string{"Please sweep.", "From Reviewer (an agent, on task #9)", "From someone since gone", "(deleted)", "From you", "On it."} {
					if !strings.Contains(string(c.entries[3].Body), want) {
						t.Errorf("read_thread answered %s, without %q", c.entries[3].Body, want)
					}
				}
			}},
		{name: "a thread too long to show whole", branch: "tidy", thread: theirs,
			threads: map[uuid.UUID]*thread{theirs: {chat: tidy, messages: long(40)}},
			turns:   []harness.Turn{calling(call("a", "read_thread", `{}`)), done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				body := string(c.entries[2].Body)
				if !strings.Contains(body, "older messages left out") || !strings.Contains(body, "message 39") || strings.Contains(body, "message 0 ") ||
					len(body) > harness.MaxOutput+200 {
					t.Errorf("read_thread answered %d bytes: %.120s", len(body), body)
				}
			}},
		{name: "no thread yet", branch: "tidy", turns: []harness.Turn{calling(call("a", "read_thread", `{}`)), done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				resultAt(2, "The task has no thread yet: post_message starts one.", false)(t, c, nil, nil)
			}},
		{name: "an issue's task talks in its chat", issue: 12,
			turns: []harness.Turn{calling(call("a", "post_message", `{"body":"Looking."}`)), done},
			check: func(t *testing.T, c *cobranch, m *model) {
				for _, th := range c.threads {
					if th.chat != (harness.Chat{Issue: 12}) {
						t.Errorf("started a thread of %+v", th.chat)
					}
				}
				if !strings.Contains(m.requests[0].System, "issue #12's chat") {
					t.Errorf("the system prompt %q doesn't name the chat", m.requests[0].System)
				}
			}},
		{name: "a refused post", branch: "tidy", postErr: errors.New("the API answered 403, forbidden"),
			turns: []harness.Turn{calling(call("a", "post_message", `{"body":"Hi."}`)), done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				resultAt(2, "post_message failed: starting the thread: the API answered 403", true)(t, c, nil, nil)
			}},
		{name: "an empty post", branch: "tidy", turns: []harness.Turn{calling(call("a", "post_message", `{"body":" "}`)), done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				resultAt(2, "post_message failed: the message is empty", true)(t, c, nil, nil)
			}},
		{name: "a task without a chat", turns: []harness.Turn{done},
			check: func(t *testing.T, _ *cobranch, m *model) {
				if names := toolNames(m.requests[0]); !slices.Equal(names, []string{"echo", "finish"}) {
					t.Errorf("the model is offered %v", names)
				}
			}},
	}
	tt = append(tt, checkoutCases()...)
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runChat(t, tc)
		})
	}
}

// checkoutCases are the cases of the task's workspace.
func checkoutCases() []chatCase {
	begun := entry(nil, agents.EntryInput, `{"text":"# Sweep"}`)
	return []chatCase{
		{name: "a new task's clone", branch: "tidy", checkout: &checkout{cloned: true}, turns: []harness.Turn{done},
			check: func(t *testing.T, c *cobranch, m *model) {
				if got := c.kinds(); !slices.Equal(got, kinds("input", "turn", "state")) {
					t.Errorf("the transcript's entries are %v", got)
				}
				for _, want := range []string{"The workspace holds a clone of repository website", "as agent@agents.example", "push with git",
					"a thread of branch tidy's chat"} {
					if !strings.Contains(m.requests[0].System, want) {
						t.Errorf("the system prompt %q doesn't say %q", m.requests[0].System, want)
					}
				}
			}},
		{name: "a task under way gets a fresh clone", branch: "tidy", checkout: &checkout{cloned: true}, entries: begun, turns: []harness.Turn{done},
			check: func(t *testing.T, c *cobranch, m *model) {
				if got := c.kinds(); !slices.Equal(got, kinds("input", "input", "turn", "state")) {
					t.Fatalf("the transcript's entries are %v", got)
				}
				if body := string(c.entries[1].Body); !strings.Contains(body, "fresh clone") || !strings.Contains(body, `"by":"harness"`) {
					t.Errorf("the harness wrote %s", body)
				}
				if got := m.requests[0].Messages[0].Text; !strings.Contains(got, "fresh clone") {
					t.Errorf("the model reads %q", got)
				}
			}},
		{name: "a task under way keeps its clone", branch: "tidy", checkout: &checkout{}, entries: begun, turns: []harness.Turn{done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				if got := c.kinds(); !slices.Equal(got, kinds("input", "turn", "state")) {
					t.Errorf("the transcript's entries are %v", got)
				}
			}},
		{name: "a clone that fails", branch: "tidy", checkout: &checkout{err: errors.New("no route to host")}, turns: []harness.Turn{done}, wantErr: true,
			check: func(t *testing.T, c *cobranch, _ *model) {
				if len(c.entries) != 0 {
					t.Errorf("the transcript's entries are %v", c.kinds())
				}
			}},
	}
}

// runChat works tc's task with an echo tool, and checks what it leaves.
func runChat(t *testing.T, tc chatCase) {
	c := &cobranch{entries: slices.Clone(tc.entries), threads: tc.threads, postErr: tc.postErr, work: agents.Work{OrgSlug: "acme", IssueNumber: tc.issue,
		AgentEmail: "agent@agents.example", Task: agents.Task{Number: 7, Title: "Sweep", State: agents.TaskRunning, Agent: agents.Principal{DisplayName: "Scout"},
			RepoSlug: "website", Branch: tc.branch, ThreadID: tc.thread}}}
	m := &model{turns: tc.turns}
	h := harness.New(c, m, 0, &echo{})
	if tc.checkout != nil {
		h.WithCheckout(tc.checkout)
	}
	if err := h.Run(context.Background()); (err != nil) != tc.wantErr {
		t.Fatalf("Run() = %v, want an error: %v", err, tc.wantErr)
	}
	if tc.checkout != nil && (len(tc.checkout.prepared) != 1 || tc.checkout.prepared[0].Task.RepoSlug != "website") {
		t.Errorf("prepared %+v", tc.checkout.prepared)
	}
	tc.check(t, c, m)
}

// toolNames lists the names of the tools r offers.
func toolNames(r harness.Request) []string {
	out := []string{}
	for _, s := range r.Tools {
		out = append(out, s.Name)
	}
	return out
}

// long is n messages, each of 4 KiB, numbered.
func long(n int) []harness.ChatMessage {
	out := make([]harness.ChatMessage, 0, n)
	for i := range n {
		out = append(out, harness.ChatMessage{ID: uuid.New(), Author: "Olivia", Body: "message " + strconv.Itoa(i) + " " + strings.Repeat("y", 4<<10)})
	}
	return out
}
