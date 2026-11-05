package restclient

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/harness"
)

// tasks answers a page of Scout's tasks, recording the query it took: one of its own, private to the token's assigner,
// and one redacted, private to someone else.
func (a *api) tasks(w http.ResponseWriter, r *http.Request) {
	a.chats = append(a.chats, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	answer(w, http.StatusOK, map[string][]Task{"tasks": {
		{Id: uuid.New(), Number: 9, Kind: "chat", Private: ptr(true), State: "queued", Agent: Principal{Id: agentID, DisplayName: "Scout"}, CreatedAt: at},
		{Id: uuid.New(), Number: 8, Kind: "chat", Private: ptr(true), Title: "Which tasks wait?", State: "running", Agent: Principal{Id: agentID, DisplayName: "Scout"},
			AssignedBy: &Principal{Id: assignerID, DisplayName: "Olivia"}, CreatedAt: at, StartedAt: ptr(at.Add(time.Minute))},
	}})
}

// TestConversations follows each of the assigner's private conversations as the harness does: its thread, a post, a
// reply, the cursor and the updates, at the conversation's own path.
func TestConversations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		chat harness.Chat
		path string
	}{
		{name: "organization", chat: harness.Chat{Conversation: true}, path: "/v1/orgs/acme/conversation"},
		{name: "repository", chat: harness.Chat{Conversation: true, Repo: "website"}, path: "/v1/orgs/acme/repos/website/conversation"},
		{name: "agent", chat: harness.Chat{Conversation: true, Agent: agentID}, path: "/v1/orgs/acme/agents/" + agentID.String() + "/conversation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &api{}
			message := uuid.MustParse("0190f1b2-0000-7000-8000-000000000073")
			follow(t, a, tt.chat, message)
			want := []string{
				"GET " + tt.path + "/threads/" + threadID.String(),
				`POST ` + tt.path + `/threads {"body":"Starting.","id":"` + message.String() + `"}`,
				"POST " + tt.path + "/threads/" + threadID.String() + `/messages {"body":"Done.","id":"` + message.String() + `"}`,
				"GET " + tt.path,
				"GET " + tt.path + "/updates",
			}
			if len(a.chats) != len(want) {
				t.Fatalf("the API took %q", a.chats)
			}
			for i, w := range want {
				if strings.TrimSpace(a.chats[i]) != w {
					t.Errorf("call %d = %q, want %q", i+1, a.chats[i], w)
				}
			}
		})
	}
}

// follow takes the calls of the harness on a chat through a client of a: its thread, a post, a reply, the cursor and
// the updates, each of which must succeed.
func follow(t *testing.T, a *api, chat harness.Chat, message uuid.UUID) {
	t.Helper()
	c, _ := client(t, a)
	ctx := context.Background()
	if messages, err := c.Thread(ctx, "acme", chat, threadID); err != nil || len(messages) != 3 || messages[1].Task != 7 {
		t.Errorf("Thread() = %+v, %v", messages, err)
	}
	if err := c.StartThread(ctx, "acme", chat, message, "Starting.", uuid.Nil); err != nil {
		t.Errorf("StartThread() = %v", err)
	}
	if err := c.Post(ctx, "acme", chat, threadID, message, "Done.", uuid.Nil); err != nil {
		t.Errorf("Post() = %v", err)
	}
	if after, err := c.ChatCursor(ctx, "acme", chat); err != nil || after != "c1" {
		t.Errorf("ChatCursor() = %q, %v; want c1", after, err)
	}
	if changed, next, err := c.ChatUpdates(ctx, "acme", chat, "c1"); err != nil || next != "c2" || len(changed) != 2 || changed[0] != threadID {
		t.Errorf("ChatUpdates() = %v, %q, %v", changed, next, err)
	}
}

// TestWorkInAConversation reads the task of a conversation: where it talks.
func TestWorkInAConversation(t *testing.T) {
	t.Parallel()
	a := &api{conversation: &MyTaskConversation{AgentId: ptr(agentID)}}
	c, _ := client(t, a)
	w, err := c.Work(context.Background())
	if err != nil || w.Conversation == nil || *w.Conversation != (agents.Conversation{AgentID: agentID}) {
		t.Errorf("Work() = %+v, %v; want the conversation with Scout", w.Conversation, err)
	}
}

// TestTasks lists Scout's tasks in two states, asking for at most limit of them.
func TestTasks(t *testing.T) {
	t.Parallel()
	a := &api{}
	c, _ := client(t, a)
	tasks, err := c.Tasks(context.Background(), "acme", agentID, []agents.TaskState{agents.TaskRunning, agents.TaskQueued}, 50)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("Tasks() = %+v, %v", tasks, err)
	}
	redacted, mine := tasks[0], tasks[1]
	if !redacted.Private || redacted.AssignedBy != nil || redacted.Title != "" || redacted.State != agents.TaskQueued || redacted.Number != 9 {
		t.Errorf("the redacted task = %+v", redacted)
	}
	if !mine.Private || mine.AssignedBy == nil || mine.AssignedBy.DisplayName != "Olivia" || mine.Title != "Which tasks wait?" || mine.StartedAt.IsZero() ||
		!mine.EndedAt.IsZero() || mine.Agent.ID != agentID {
		t.Errorf("the assigner's task = %+v", mine)
	}
	if !slices.Equal(a.chats, []string{"GET /v1/orgs/acme/tasks?agent=" + agentID.String() + "&state=running&state=queued&limit=50"}) {
		t.Errorf("the API took %q", a.chats)
	}
	if _, err := c.Tasks(context.Background(), "nowhere", agentID, nil, 10); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("Tasks(another organization) = %v; want the API's 404", err)
	}
}
