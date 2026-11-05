package restclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/harness"
)

// api is the REST API's calls that the harness makes, over a transcript in memory, as the token cba_valid alone.
type api struct {
	mu      sync.Mutex
	entries []TranscriptEntry
	tokens  []string
	// unavailable is how many calls answer 503 before the API answers.
	unavailable int
	// chats are the chat calls it took, each its method, its escaped path and its body.
	chats []string
	// conversation is the private conversation the token's task talks in; nil for none.
	conversation *MyTaskConversation
}

var agentID, assignerID = uuid.New(), uuid.New()

func (a *api) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokens = append(a.tokens, r.Header.Get("Authorization"))
	switch {
	case a.unavailable > 0:
		a.unavailable--
		w.WriteHeader(http.StatusServiceUnavailable)
	case r.Header.Get("Authorization") != "Bearer cba_valid":
		problem(w, http.StatusUnauthorized, "auth.unauthenticated", "The token is not valid.")
	case r.Method == http.MethodGet && r.URL.Path == "/v1/me/task":
		answer(w, http.StatusOK, MyTask{Organization: "acme", AgentInstructions: "Be brief.", AgentEmail: "scout@agents.example", IssueNumber: ptr(int64(12)), IssueKey: ptr("ACME"),
			Task: Task{Number: 7, Title: "Sweep", State: "running", Agent: Principal{Id: agentID, DisplayName: "Scout"},
				AssignedBy: &Principal{Id: assignerID, DisplayName: "Olivia"}, RepositorySlug: ptr("website"), Branch: ptr("tidy"),
				Sha: ptr("0123456789abcdef0123456789abcdef01234567"), CreatedAt: time.Unix(10, 0).UTC(), StartedAt: ptr(time.Unix(20, 0).UTC())},
			Conversation: a.conversation})
	case strings.Contains(r.URL.Path, "/chat") || strings.Contains(r.URL.Path, "/conversation"):
		a.chat(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/orgs/acme/tasks":
		a.tasks(w, r)
	case r.URL.Path != "/v1/orgs/acme/tasks/7/transcript":
		problem(w, http.StatusNotFound, "task.not_found", "No such task.")
	case r.Method == http.MethodGet:
		a.list(w, r)
	default:
		a.append(w, r)
	}
}

// list answers a page of the transcript.
func (a *api) list(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.Atoi(r.URL.Query().Get("after")) // 0 when absent
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit")) // as above
	page := a.entries[min(after, len(a.entries)):]
	page = page[:min(limit, len(page))]
	answer(w, http.StatusOK, map[string]any{"entries": page})
}

// append records the next entry, or answers the one already numbered as it is.
func (a *api) append(w http.ResponseWriter, r *http.Request) {
	var in TranscriptEntryInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		problem(w, http.StatusBadRequest, "body.invalid", err.Error())
		return
	}
	switch {
	case in.Seq <= len(a.entries):
		answer(w, http.StatusOK, a.entries[in.Seq-1])
	case in.Seq == len(a.entries)+1:
		e := TranscriptEntry{Seq: in.Seq, Kind: in.Kind, Body: in.Body, CreatedAt: time.Now().UTC()}
		a.entries = append(a.entries, e)
		answer(w, http.StatusCreated, e)
	default:
		problem(w, http.StatusConflict, "transcript.seq_conflict", "The entry skips a number.")
	}
}

func answer(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body) // the test fails on what the client reads
}

func problem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{Status: status, Code: code, Title: http.StatusText(status), Detail: &detail}) // as above
}

func ptr[T any](v T) *T { return &v }

// client is a client of a, as the token the returned file holds, which waits a millisecond between attempts.
func client(t *testing.T, a *api) (*API, string) {
	t.Helper()
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)
	file := filepath.Join(t.TempDir(), "token")
	write(t, file, "cba_valid\n")
	c, err := newClient(srv.URL+"/", file, &retrier{client: srv.Client(), backoff: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return c, file
}

func write(t *testing.T, file, token string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWork(t *testing.T) {
	t.Parallel()
	c, _ := client(t, &api{unavailable: 2})
	w, err := c.Work(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if w.OrgSlug != "acme" || w.AgentInstructions != "Be brief." || w.AgentEmail != "scout@agents.example" || w.IssueNumber != 12 || w.IssueKey != "ACME" || w.Task.Number != 7 || w.Task.State != agents.TaskRunning || w.Task.Agent.ID != agentID ||
		w.Task.Agent.DisplayName != "Scout" || w.Task.AssignedBy == nil || w.Task.AssignedBy.DisplayName != "Olivia" || w.Task.RepoSlug != "website" ||
		w.Task.Branch != "tidy" || w.Task.ThreadID != uuid.Nil || w.Task.SHA != "0123456789abcdef0123456789abcdef01234567" ||
		!w.Task.CreatedAt.Equal(time.Unix(10, 0)) || !w.Task.StartedAt.Equal(time.Unix(20, 0)) {
		t.Errorf("Work() = %+v", w)
	}
}

func TestTranscript(t *testing.T) {
	t.Parallel()
	a := &api{}
	c, _ := client(t, a)
	ctx := context.Background()
	for seq := 1; seq <= transcriptPage+1; seq++ {
		e := agents.Entry{Seq: seq, Kind: agents.EntryInput, Body: json.RawMessage(fmt.Sprintf(`{"text":"%d"}`, seq))}
		if got, err := c.Append(ctx, "acme", 7, e); err != nil || got.Seq != seq || got.Kind != agents.EntryInput || got.CreatedAt.IsZero() {
			t.Fatalf("Append(%d) = %+v, %v", seq, got, err)
		}
	}
	again, err := c.Append(ctx, "acme", 7, agents.Entry{Seq: 2, Kind: agents.EntryTurn, Body: json.RawMessage(`{}`)})
	if err != nil || again.Kind != agents.EntryInput || string(again.Body) != `{"text":"2"}` {
		t.Errorf("Append(again) = %+v, %v; want the entry already there", again, err)
	}
	if _, err := c.Append(ctx, "acme", 7, agents.Entry{Seq: 600, Kind: agents.EntryTurn, Body: json.RawMessage(`{}`)}); err == nil ||
		!strings.Contains(err.Error(), "409, transcript.seq_conflict: The entry skips a number.") {
		t.Errorf("Append(a skip) = %v", err)
	}
	a.mu.Lock()
	calls := len(a.tokens)
	a.mu.Unlock()
	all, err := c.Transcript(ctx, "acme", 7)
	if err != nil || len(all) != transcriptPage+1 || all[transcriptPage].Seq != transcriptPage+1 || string(all[0].Body) != `{"text":"1"}` {
		t.Fatalf("Transcript() = %d entries, %v", len(all), err)
	}
	if pages := len(a.tokens) - calls; pages != 2 {
		t.Errorf("Transcript() read %d pages, want 2", pages)
	}
	if _, err := c.Transcript(ctx, "acme", 8); err == nil || !strings.Contains(err.Error(), "404, task.not_found") {
		t.Errorf("Transcript(another task) = %v", err)
	}
}

func TestTokenFile(t *testing.T) {
	t.Parallel()
	a := &api{}
	c, file := client(t, a)
	ctx := context.Background()
	write(t, file, "cba_expired")
	if _, err := c.Work(ctx); !errors.Is(err, harness.ErrRefused) || !strings.Contains(err.Error(), "The token is not valid.") {
		t.Errorf("Work(an expired token) = %v, want %v", err, harness.ErrRefused)
	}
	write(t, file, "cba_valid")
	if _, err := c.Work(ctx); err != nil {
		t.Errorf("Work(the renewed token) = %v", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Work(ctx); err == nil || !strings.Contains(err.Error(), "reading the task's token") {
		t.Errorf("Work(no token) = %v", err)
	}
	if err := refused("reading", http.StatusUnauthorized, []byte("the credential acts as nobody\n")); err.Error() != "reading: cobranch refused the task's token: the credential acts as nobody" {
		t.Errorf("refused(a plain answer) = %v", err)
	}
	if want := []string{"Bearer cba_expired", "Bearer cba_valid"}; strings.Join(a.tokens, ",") != strings.Join(want, ",") {
		t.Errorf("the API was sent %v, want %v", a.tokens, want)
	}
}

func TestRetries(t *testing.T) {
	t.Parallel()
	a := &api{unavailable: attempts}
	c, _ := client(t, a)
	if _, err := c.Append(context.Background(), "acme", 7, agents.Entry{Seq: 1, Kind: agents.EntryInput, Body: json.RawMessage(`{"text":"Go."}`)}); err == nil ||
		!strings.Contains(err.Error(), "503") || len(a.tokens) != attempts {
		t.Errorf("Append(while the API is unavailable) = %v after %d calls", err, len(a.tokens))
	}
	a.unavailable = 1
	if e, err := c.Append(context.Background(), "acme", 7, agents.Entry{Seq: 1, Kind: agents.EntryInput, Body: json.RawMessage(`{"text":"Go."}`)}); err != nil || e.Seq != 1 {
		t.Errorf("Append(after an outage) = %+v, %v; want the body sent again whole", e, err)
	}
}

// threadID is the one thread the chats hold; forbiddenID one whose posts the API refuses.
var threadID, forbiddenID = uuid.MustParse("0190f1b2-0000-7000-8000-000000000071"), uuid.MustParse("0190f1b2-0000-7000-8000-000000000072")

// chat answers a chat call, recording it: thread threadID holds a person's message and one of task 7's.
func (a *api) chat(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body) // a test's body always reads
	a.chats = append(a.chats, r.Method+" "+r.URL.EscapedPath()+" "+strings.TrimSpace(string(body)))
	switch {
	case strings.Contains(r.URL.Path, forbiddenID.String()):
		problem(w, http.StatusForbidden, "auth.forbidden", "You may not post here.")
	case strings.HasSuffix(r.URL.Path, "/chat") || strings.HasSuffix(r.URL.Path, "/conversation"):
		answer(w, http.StatusOK, map[string]any{"after": "c1", "threads": []any{}})
	case strings.HasSuffix(r.URL.Path, "/updates") && r.URL.Query().Get("after") == "gone":
		problem(w, http.StatusBadRequest, "chat.cursor_invalid", "That cursor isn't one.")
	case strings.HasSuffix(r.URL.Path, "/updates"):
		other := uuid.MustParse("0190f1b2-0000-7000-8000-000000000074")
		answer(w, http.StatusOK, map[string]any{"after": "c2", "updates": []map[string]any{
			{"cursor": "u1", "kind": "message.posted", "thread": map[string]any{"id": threadID}},
			{"cursor": "u2", "kind": "message.posted", "thread": map[string]any{"id": other}},
			{"cursor": "u3", "kind": "message.edited", "thread": map[string]any{"id": threadID}}}})
	case r.Method == http.MethodGet:
		at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
		answer(w, http.StatusOK, ChatThread{Id: threadID, CreatedAt: at, Messages: &[]ChatMessage{
			{Id: uuid.New(), ThreadId: threadID, Body: "Please sweep.", Author: &Principal{Id: assignerID, DisplayName: "Olivia"}, CreatedAt: at},
			{Id: uuid.New(), ThreadId: threadID, Body: "On it.", Author: &Principal{Id: agentID, DisplayName: "Scout"}, CreatedAt: at, TaskNumber: ptr(int64(7))},
			{Id: uuid.New(), ThreadId: threadID, CreatedAt: at, DeletedAt: &at}}})
	case strings.HasSuffix(r.URL.Path, "/messages"):
		answer(w, http.StatusCreated, ChatMessage{Id: uuid.New(), ThreadId: threadID})
	default:
		answer(w, http.StatusCreated, ChatThread{Id: threadID})
	}
}

func TestChats(t *testing.T) {
	t.Parallel()
	a := &api{}
	c, _ := client(t, a)
	ctx, message := context.Background(), uuid.MustParse("0190f1b2-0000-7000-8000-000000000073")
	branch, issue := harness.Chat{Repo: "website", Branch: "fix/header"}, harness.Chat{Issue: 12}
	messages, err := c.Thread(ctx, "acme", branch, threadID)
	if err != nil || len(messages) != 3 || messages[0].Author != "Olivia" || messages[0].Body != "Please sweep." || messages[0].Task != 0 ||
		messages[1].Task != 7 || !messages[2].Deleted || messages[2].Author != "" {
		t.Errorf("Thread() = %+v, %v", messages, err)
	}
	if err := c.StartThread(ctx, "acme", branch, message, "Starting."); err != nil {
		t.Errorf("StartThread() = %v", err)
	}
	if err := c.Post(ctx, "acme", issue, threadID, message, "Swept."); err != nil {
		t.Errorf("Post() = %v", err)
	}
	if _, err := c.Thread(ctx, "acme", issue, threadID); err != nil {
		t.Errorf("Thread() of an issue's chat = %v", err)
	}
	if err := c.Post(ctx, "acme", branch, forbiddenID, message, "Hi."); err == nil || !strings.Contains(err.Error(), "403") ||
		!strings.Contains(err.Error(), "You may not post here.") || errors.Is(err, harness.ErrRefused) {
		t.Errorf("Post() where it may not = %v", err)
	}
	checkUpdates(t, c, branch, issue)
	want := []string{
		"GET /v1/orgs/acme/repos/website/branches/fix%2Fheader/chat/threads/" + threadID.String() + " ",
		`POST /v1/orgs/acme/repos/website/branches/fix%2Fheader/chat/threads {"body":"Starting.","id":"` + message.String() + `"}`,
		"POST /v1/orgs/acme/issues/12/chat/threads/" + threadID.String() + `/messages {"body":"Swept.","id":"` + message.String() + `"}`,
		"GET /v1/orgs/acme/issues/12/chat/threads/" + threadID.String() + " ",
		"POST /v1/orgs/acme/repos/website/branches/fix%2Fheader/chat/threads/" + forbiddenID.String() + `/messages {"body":"Hi.","id":"` + message.String() + `"}`,
		"GET /v1/orgs/acme/repos/website/branches/fix%2Fheader/chat ",
		"GET /v1/orgs/acme/issues/12/chat ",
		"GET /v1/orgs/acme/repos/website/branches/fix%2Fheader/chat/updates ",
		"GET /v1/orgs/acme/issues/12/chat/updates ",
	}
	if len(a.chats) < len(want) {
		t.Fatalf("the API took %q", a.chats)
	}
	for i, w := range want {
		if strings.TrimSpace(a.chats[i]) != strings.TrimSpace(w) {
			t.Errorf("call %d = %q, want %q", i+1, a.chats[i], w)
		}
	}
}

// checkUpdates checks how the client follows a branch's chat and an issue's: a cursor from a page of one thread, and
// the threads the updates after it changed, each once, waiting the most the REST server waits.
func checkUpdates(t *testing.T, c *API, branch, issue harness.Chat) {
	t.Helper()
	ctx := context.Background()
	for _, chat := range []harness.Chat{branch, issue} {
		if after, err := c.ChatCursor(ctx, "acme", chat); err != nil || after != "c1" {
			t.Errorf("ChatCursor(%+v) = %q, %v; want c1", chat, after, err)
		}
	}
	other := uuid.MustParse("0190f1b2-0000-7000-8000-000000000074")
	for _, chat := range []harness.Chat{branch, issue} {
		changed, next, err := c.ChatUpdates(ctx, "acme", chat, "c1")
		if err != nil || next != "c2" || !slices.Equal(changed, []uuid.UUID{threadID, other}) {
			t.Errorf("ChatUpdates(%+v) = %v, %q, %v; want both threads once, then c2", chat, changed, next, err)
		}
	}
	if _, _, err := c.ChatUpdates(ctx, "acme", branch, "gone"); err == nil || !strings.Contains(err.Error(), "That cursor isn't one.") {
		t.Errorf("ChatUpdates(a cursor that isn't one) = %v", err)
	}
}
