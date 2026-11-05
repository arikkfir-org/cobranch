package harness_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/harness"
)

// scout is the agent behind the task TestChat runs, and other another agent of its organization.
var (
	scout = uuid.MustParse("0190f1b2-0000-7000-8000-00000000005c")
	other = uuid.MustParse("0190f1b2-0000-7000-8000-0000000000e7")
)

// conversationCases are TestChat's cases of a task asked in its assigner's private conversation: the conversation's
// chat, what the prompt says of it, and the tasks tool of a conversation with the task's own agent.
func conversationCases() []chatCase {
	posting := []harness.Turn{calling(call("a", "post_message", `{"body":"On it."}`)), done}
	return []chatCase{
		{name: "a task asked in a conversation with its agent talks there and lists its tasks", conversation: &agents.Conversation{AgentID: scout},
			tasks: queue(), turns: []harness.Turn{calling(call("a", "post_message", `{"body":"On it."}`)), calling(call("b", "tasks", `{}`)), done},
			check: func(t *testing.T, c *cobranch, m *model) {
				talkedIn(t, c, harness.Chat{Conversation: true, Agent: scout})
				containsAll(t, m.requests[0].System, "private conversation with you", "tasks lists what you work on")
				if names := toolNames(m.requests[0]); !slices.Contains(names, "tasks") {
					t.Errorf("the model is offered %v, without tasks", names)
				}
				listed := lastResult(m.requests[2])
				containsAll(t, listed,
					"Working on now:\n- #5 running since 2026-11-05 08:00 UTC, asked for in private by someone else\n"+
						"- #7 (this one) running since 2026-11-05 08:30 UTC: Sweep, asked by Olivia, in website (private to who asked)\n",
					"Your queue, in the order you will take it:\n- 1. #8 queued since 2026-11-05 09:00 UTC: Add tests, asked by Noah, on branch tidy of website\n"+
						"- 2. #9 queued since 2026-11-05 09:30 UTC, asked for in private by someone else\n",
					"Ended last:\n- #3 succeeded since 2026-11-04 10:00 UTC: Fix the footer, asked by Olivia, in website")
				if strings.Contains(listed, "#4") {
					t.Errorf("the tool listed another agent's task: %s", listed)
				}
			}},
		{name: "an agent without tasks lists none", conversation: &agents.Conversation{AgentID: scout},
			turns: []harness.Turn{calling(call("a", "tasks", `{}`)), done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				resultAt(2, "Working on now:\nnone\n\nYour queue, in the order you will take it:\nnone\n\nEnded last:\nnone", false)(t, c, nil, nil)
			}},
		{name: "a refused listing of tasks", conversation: &agents.Conversation{AgentID: scout}, tasksErr: errors.New("the API answered 500"),
			turns: []harness.Turn{calling(call("a", "tasks", `{}`)), done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				resultAt(2, "tasks failed: listing the tasks you work on: the API answered 500", true)(t, c, nil, nil)
			}},
		{name: "a task asked in a conversation with another agent lists no tasks", conversation: &agents.Conversation{AgentID: other},
			turns: posting,
			check: func(t *testing.T, c *cobranch, m *model) {
				talkedIn(t, c, harness.Chat{Conversation: true, Agent: other})
				containsAll(t, m.requests[0].System, "private conversation with another agent")
				if names := toolNames(m.requests[0]); slices.Contains(names, "tasks") {
					t.Errorf("the model is offered %v, with tasks", names)
				}
			}},
		{name: "a task asked in a conversation about a repository", conversation: &agents.Conversation{RepoSlug: "website"}, branch: "tasks/7",
			turns: posting,
			check: func(t *testing.T, c *cobranch, m *model) {
				talkedIn(t, c, harness.Chat{Conversation: true, Repo: "website"})
				containsAll(t, m.requests[0].System, "private conversation about repository website")
			}},
		{name: "a task asked in a conversation about the organization", conversation: &agents.Conversation{}, turns: posting,
			check: func(t *testing.T, c *cobranch, m *model) {
				talkedIn(t, c, harness.Chat{Conversation: true})
				containsAll(t, m.requests[0].System, "private conversation about the organization")
			}},
	}
}

// queue is the organization's tasks, oldest first: scout's, ended, running and queued, some private to others, and
// another agent's.
func queue() []agents.Task {
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 11, day, hour, minute, 0, 0, time.UTC) }
	olivia, noah := &agents.Principal{ID: uuid.New(), DisplayName: "Olivia"}, &agents.Principal{ID: uuid.New(), DisplayName: "Noah"}
	me, them := agents.Principal{ID: scout, DisplayName: "Scout"}, agents.Principal{ID: other, DisplayName: "Builder"}
	return []agents.Task{
		{Number: 3, Agent: me, Title: "Fix the footer", AssignedBy: olivia, RepoSlug: "website", State: agents.TaskSucceeded,
			CreatedAt: at(4, 8, 0), StartedAt: at(4, 9, 0), EndedAt: at(4, 10, 0)},
		{Number: 4, Agent: them, Title: "Elsewhere", AssignedBy: noah, State: agents.TaskFailed, CreatedAt: at(4, 9, 0), EndedAt: at(4, 11, 0)},
		{Number: 5, Agent: me, Private: true, State: agents.TaskRunning, CreatedAt: at(5, 7, 0), StartedAt: at(5, 8, 0)},
		{Number: 7, Agent: me, Title: "Sweep", AssignedBy: olivia, RepoSlug: "website", Private: true, State: agents.TaskRunning,
			CreatedAt: at(5, 8, 0), StartedAt: at(5, 8, 30)},
		{Number: 8, Agent: me, Title: "Add tests", AssignedBy: noah, RepoSlug: "website", Branch: "tidy", State: agents.TaskQueued, CreatedAt: at(5, 9, 0)},
		{Number: 9, Agent: me, Private: true, State: agents.TaskQueued, CreatedAt: at(5, 9, 30)},
	}
}

// queueCases are TestChat's cases of the tasks tool's queue: one longer than the tool lists, and one it can't read.
func queueCases() []chatCase {
	return []chatCase{
		{name: "a queue longer than the tool lists shows its head and how many follow", conversation: &agents.Conversation{AgentID: scout},
			tasks: longQueue(), turns: []harness.Turn{calling(call("a", "tasks", `{}`)), done},
			check: func(t *testing.T, _ *cobranch, m *model) {
				listed := lastResult(m.requests[1])
				containsAll(t, listed, "Your queue, in the order you will take it:\n- 1. #8 queued", "- 50. #57 queued since",
					"- 50. #57 queued since 2026-11-05 10:00 UTC: More, asked by Noah\n- and 3 more after them\n\nEnded last:")
				if strings.Contains(listed, "#58") {
					t.Errorf("the tool listed past the head of the queue: %s", listed)
				}
			}},
		{name: "a refused listing of the queue", conversation: &agents.Conversation{AgentID: scout}, queueErr: errors.New("the API answered 500"),
			turns: []harness.Turn{calling(call("a", "tasks", `{}`)), done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				resultAt(2, "tasks failed: listing your queue: the API answered 500", true)(t, c, nil, nil)
			}},
	}
}

// longQueue is queue() with 51 more tasks queued after it, #10 to #60, so the queue holds 53: more than the tasks tool
// lists.
func longQueue() []agents.Task {
	tasks := queue()
	noah := &agents.Principal{ID: uuid.New(), DisplayName: "Noah"}
	for n := int64(10); n <= 60; n++ {
		tasks = append(tasks, agents.Task{Number: n, Agent: agents.Principal{ID: scout, DisplayName: "Scout"}, Title: "More", AssignedBy: noah,
			State: agents.TaskQueued, CreatedAt: time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)})
	}
	return tasks
}

// talkedIn checks that the task started one thread, of the chat named.
func talkedIn(t *testing.T, c *cobranch, chat harness.Chat) {
	t.Helper()
	if len(c.threads) != 1 {
		t.Fatalf("started %d threads", len(c.threads))
	}
	for id, th := range c.threads {
		if th.chat != chat {
			t.Errorf("thread %s is of %+v, want %+v", id, th.chat, chat)
		}
	}
}

// lastResult is the text of the first result of the last message of r.
func lastResult(r harness.Request) string {
	m := r.Messages[len(r.Messages)-1]
	if len(m.Results) == 0 {
		return ""
	}
	return m.Results[0].Text
}
