package restclient

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
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
	if q := r.URL.Query(); a.queued > 0 && slices.Equal(q["state"], []string{"queued"}) {
		a.queuePage(w, q)
		return
	}
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	answer(w, http.StatusOK, map[string][]Task{"tasks": {
		{Id: uuid.New(), Number: 9, Kind: "chat", Private: ptr(true), State: "queued", Agent: Principal{Id: agentID, DisplayName: "Scout"}, CreatedAt: at},
		{Id: uuid.New(), Number: 8, Kind: "chat", Private: ptr(true), Title: "Which tasks wait?", State: "running", Agent: Principal{Id: agentID, DisplayName: "Scout"},
			AssignedBy: &Principal{Id: assignerID, DisplayName: "Olivia"}, CreatedAt: at, StartedAt: ptr(at.Add(time.Minute))},
	}})
}

// queuePage answers a page of Scout's queue, newest first: from the newest, or below the cursor, at most limit tasks,
// linking the next page while older ones remain.
func (a *api) queuePage(w http.ResponseWriter, q url.Values) {
	before, limit := int64(a.queued+1), 100
	if c, err := strconv.ParseInt(q.Get("cursor"), 10, 64); err == nil {
		before = c
	}
	if l, err := strconv.Atoi(q.Get("limit")); err == nil {
		limit = l
	}
	var page []Task
	for n := before - 1; n >= 1 && len(page) < limit; n-- {
		page = append(page, Task{Id: uuid.New(), Number: n, Kind: "chat", Title: "Task " + strconv.FormatInt(n, 10), State: "queued",
			Agent: Principal{Id: agentID, DisplayName: "Scout"}, CreatedAt: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)})
	}
	if last := page[len(page)-1].Number; last > 1 {
		w.Header().Set("Link", `<?`+url.Values{"cursor": {strconv.FormatInt(last, 10)}, "limit": {strconv.Itoa(limit)}, "state": {"queued"}}.Encode()+`>; rel="next"`)
	}
	answer(w, http.StatusOK, map[string][]Task{"tasks": page})
}

// TestQueue reads the head of Scout's queue, oldest first, however many pages the queue takes, and how many it holds.
func TestQueue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		queued, limit int
		want          []int64
		pages         int
	}{
		{name: "a queue on one page", queued: 3, limit: 50, want: []int64{1, 2, 3}, pages: 1},
		{name: "a queue longer than the head asked for", queued: 7, limit: 3, want: []int64{1, 2, 3}, pages: 1},
		{name: "a queue over several pages", queued: 250, limit: 50, want: numbers(1, 50), pages: 3},
		{name: "a queue that ends on a page's last task", queued: 200, limit: 2, want: []int64{1, 2}, pages: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := &api{queued: tc.queued}
			c, _ := client(t, a)
			head, total, err := c.Queue(context.Background(), "acme", agentID, tc.limit)
			if err != nil {
				t.Fatalf("Queue() = %v", err)
			}
			got := make([]int64, 0, len(head))
			for _, task := range head {
				got = append(got, task.Number)
			}
			if !slices.Equal(got, tc.want) || total != tc.queued {
				t.Errorf("Queue() = %v, %d; want %v, %d", got, total, tc.want, tc.queued)
			}
			if len(a.chats) != tc.pages || !strings.Contains(a.chats[0], "state=queued") || !strings.Contains(a.chats[0], "limit=100") {
				t.Errorf("the API took %q; want %d pages of the queued tasks", a.chats, tc.pages)
			}
		})
	}
	if _, _, err := (func() ([]agents.Task, int, error) {
		c, _ := client(t, &api{})
		return c.Queue(context.Background(), "nowhere", agentID, 10)
	})(); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("Queue(another organization) = %v; want the API's 404", err)
	}
}

// numbers are the numbers from first to last, in order.
func numbers(first, last int64) []int64 {
	var out []int64
	for n := first; n <= last; n++ {
		out = append(out, n)
	}
	return out
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
	if err := c.StartThread(ctx, "acme", chat, message, "Starting."); err != nil {
		t.Errorf("StartThread() = %v", err)
	}
	if err := c.Post(ctx, "acme", chat, threadID, message, "Done."); err != nil {
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
