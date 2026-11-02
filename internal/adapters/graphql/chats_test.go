package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

// chatStep is one document of a conversation on feature/login's chat, against one fixture: it may name {repo},
// {thread} and {message}, the website's ID and the last thread started and message posted.
type chatStep struct {
	name                 string
	viewer               func(f *orgFixture) *auth.Authenticated
	document             string
	want                 []string
	wantCode, wantReason string
}

// maxAs is Max, holding role on the website.
func maxAs(role uuid.UUID) func(*orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		f.store.Grant(f.max.User.ID, role, auth.ScopeRepository, f.website.ID)
		return &f.max
	}
}

// startedID finds the ID a startChatThread or postChatMessage answered with.
var startedID = regexp.MustCompile(`"(startChatThread|postChatMessage)":\{"id":"([0-9a-f-]{36})"`)

func TestChats(t *testing.T) {
	const target = `repositoryId: "{repo}", branch: "feature/login"`
	const chat = `{ organization(slug: "acme") { repository(slug: "website") { chat(branch: "feature/login") { after nextCursor threads { id anchor { path startLine endLine } resolvedBy { displayName } messages { body deletedBy { displayName } } } } } } }`
	anchor := `anchor: {sha: "` + shaOf("c") + `", path: "README.md", startLine: 1, endLine: 2}`
	steps := []chatStep{
		{name: "an Owner starts an anchored thread", viewer: olivia,
			document: `mutation { startChatThread(` + target + `, input: {body: "Why *this*?", ` + anchor + `}) { id outdated startedBy { displayName } version messages { html } } }`,
			want:     []string{`"outdated":false`, `"startedBy":{"displayName":"Olivia"}`, `"version":1`, `"html":"\u003cp\u003eWhy \u003cem\u003ethis\u003c/em\u003e?\u003c/p\u003e\n"`}},
		{name: "a Reader reads the chat", viewer: maxAs(auth.RoleReader), document: chat,
			want: []string{`"after":"1","nextCursor":null`, `"id":"{thread}","anchor":{"path":"README.md","startLine":1,"endLine":2}`}},
		{name: "a Reader may not post", viewer: maxAs(auth.RoleReader),
			document: `mutation { postChatMessage(` + target + `, thread: "{thread}", input: {body: "Hi"}) { id } }`, wantCode: forbidden},
		{name: "a Developer replies", viewer: maxAs(auth.RoleDeveloper),
			document: `mutation { postChatMessage(` + target + `, thread: "{thread}", input: {body: "Because"}) { id threadId body author { displayName } version } }`,
			want:     []string{`"threadId":"{thread}","body":"Because","author":{"displayName":"Max"},"version":1`}},
		{name: "another's ID", viewer: olivia,
			document: `mutation { postChatMessage(` + target + `, thread: "{thread}", input: {id: "{message}", body: "Mine"}) { id } }`,
			wantCode: conflict, wantReason: "chat.id_taken"},
		{name: "its author edits it", viewer: maxAs(auth.RoleDeveloper),
			document: `mutation { editChatMessage(` + target + `, thread: "{thread}", message: "{message}", body: "Because, really", expectedVersion: 1) { body editedAt version } }`,
			want:     []string{`"body":"Because, really","editedAt":"`, `"version":2`}},
		{name: "a stale edit", viewer: maxAs(auth.RoleDeveloper),
			document: `mutation { editChatMessage(` + target + `, thread: "{thread}", message: "{message}", body: "Stale", expectedVersion: 1) { body } }`,
			wantCode: conflict, wantReason: "precondition.failed"},
		{name: "another may not edit it", viewer: olivia,
			document: `mutation { editChatMessage(` + target + `, thread: "{thread}", message: "{message}", body: "Mine now") { body } }`, wantCode: forbidden},
		{name: "its starter resolves the thread", viewer: olivia,
			document: `mutation { resolveChatThread(` + target + `, thread: "{thread}", resolved: true, expectedVersion: 1) { resolvedBy { displayName } version messages { id } } }`,
			want:     []string{`"resolvedBy":{"displayName":"Olivia"},"version":2,"messages":[{"id":"`}},
		{name: "an Owner moderates a reply away", viewer: olivia,
			document: `mutation { deleteChatMessage(` + target + `, thread: "{thread}", message: "{message}") }`, want: []string{`{"data":{"deleteChatMessage":true}}`}},
		{name: "the deleted reply keeps its place", viewer: olivia, document: chat,
			want: []string{`"resolvedBy":{"displayName":"Olivia"},"messages":[{"body":"Why *this*?","deletedBy":null},{"body":"","deletedBy":{"displayName":"Olivia"}}]`}},
		{name: "a deleted reply takes no edit", viewer: maxAs(auth.RoleDeveloper),
			document: `mutation { editChatMessage(` + target + `, thread: "{thread}", message: "{message}", body: "Back") { body } }`,
			wantCode: conflict, wantReason: "message.deleted"},
	}
	f := newOrgFixture()
	var ids chatIDs
	for _, s := range steps {
		s.run(t, f, &ids)
	}
}

// chatIDs are the last thread started and message posted.
type chatIDs struct{ thread, message string }

// run sends the step's document, keeping in ids the thread it starts or the message it posts.
func (s chatStep) run(t *testing.T, f *orgFixture, ids *chatIDs) {
	t.Helper()
	expand := func(text string) string {
		return strings.NewReplacer("{repo}", f.website.ID.String(), "{thread}", ids.thread, "{message}", ids.message).Replace(text)
	}
	body := f.query(s.viewer(f), expand(s.document))
	if m := startedID.FindStringSubmatch(body); m != nil && m[1] == "startChatThread" {
		ids.thread = m[2]
	} else if m != nil {
		ids.message = m[2]
	}
	if s.wantCode == "" && strings.Contains(body, `"errors"`) {
		t.Errorf("%s: body = %s, want no errors", s.name, body)
	}
	for _, w := range (orgCase{want: s.want, wantCode: s.wantCode, wantReason: s.wantReason}).wanted() {
		if !strings.Contains(body, expand(w)) {
			t.Errorf("%s: body = %s, want %s in it", s.name, body, expand(w))
		}
	}
}

func TestChatErrors(t *testing.T) {
	mutation := func(field string) func(*orgFixture) string {
		return func(f *orgFixture) string {
			return strings.ReplaceAll(`mutation { `+field+` }`, "{repo}", f.website.ID.String())
		}
	}
	const target = `repositoryId: "{repo}", branch: "feature/login"`
	runOrgCases(t, []orgCase{
		{name: "a blank thread", viewer: olivia, document: mutation(`startChatThread(` + target + `, input: {body: " "}) { id }`),
			wantCode: badUserInput, wantReason: "body.empty"},
		{name: "a commit the repository lacks", viewer: olivia,
			document: mutation(`startChatThread(` + target + `, input: {body: "Hm", anchor: {sha: "` + shaOf("f") + `", path: "a", startLine: 1, endLine: 1}}) { id }`),
			wantCode: badUserInput, wantReason: "anchor.commit_not_found"},
		{name: "an ID that is no UUID", viewer: olivia, document: mutation(`startChatThread(` + target + `, input: {id: "nope", body: "Hi"}) { id }`),
			wantCode: badUserInput, wantReason: "id.invalid"},
		{name: "a version before the first", viewer: olivia,
			document: mutation(`resolveChatThread(` + target + `, thread: "` + uuid.NewString() + `", resolved: false, expectedVersion: 0) { id }`),
			wantCode: badUserInput, wantReason: "version.invalid"},
		{name: "no such thread", viewer: olivia,
			document: mutation(`postChatMessage(` + target + `, thread: "` + uuid.NewString() + `", input: {body: "Hi"}) { id }`),
			wantCode: notFound, wantReason: "thread.not_found"},
		{name: "a cursor of no page", viewer: olivia,
			document: fixed(`{ organization(slug: "acme") { repository(slug: "website") { chat(branch: "feature/login", after: "nope") { after } } } }`),
			wantCode: badUserInput, wantReason: "cursor.invalid"},
		{name: "a branch that doesn't live has no chat", viewer: olivia,
			document: fixed(`{ organization(slug: "acme") { repository(slug: "website") { chat(branch: "gone") { after } } } }`), want: []string{`"chat":null`}},
		{name: "an outsider finds no branch", viewer: nia, document: mutation(`startChatThread(` + target + `, input: {body: "Hi"}) { id }`),
			wantCode: notFound, wantReason: "branch.not_found"},
	})
}

// wsMessage is a message of graphql-transport-ws.
type wsMessage struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// wsClient is a graphql-transport-ws client of a test's.
type wsClient struct {
	t    *testing.T
	ctx  context.Context
	conn *websocket.Conn
}

// dialWS opens a WebSocket to url from a page of origin, perhaps none.
func dialWS(ctx context.Context, url, origin string) (*websocket.Conn, error) {
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: []string{"graphql-transport-ws"}, HTTPHeader: header})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close() // an upgrade's body is empty, and a refusal's is read by nobody
	}
	if err != nil && resp != nil {
		return nil, fmt.Errorf("status %d: %w", resp.StatusCode, err)
	}
	return conn, err //nolint:wrapcheck // the test reports it as it is
}

func (c *wsClient) send(m wsMessage) {
	c.t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := c.conn.Write(c.ctx, websocket.MessageText, data); err != nil {
		c.t.Fatal(err)
	}
}

// read reads the next message but a ping, which it answers.
func (c *wsClient) read() wsMessage {
	c.t.Helper()
	for {
		_, data, err := c.conn.Read(c.ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		var m wsMessage
		if err := json.Unmarshal(data, &m); err != nil {
			c.t.Fatal(err)
		}
		if m.Type != "ping" {
			return m
		}
		c.send(wsMessage{Type: "pong"})
	}
}

// subscribe starts subscription 1 to document, once the server acknowledges the connection.
func (c *wsClient) subscribe(document string) {
	c.t.Helper()
	c.send(wsMessage{Type: "connection_init"})
	if m := c.read(); m.Type != "connection_ack" {
		c.t.Fatalf("first message = %+v, want connection_ack", m)
	}
	payload, err := json.Marshal(map[string]string{"query": document})
	if err != nil {
		c.t.Fatal(err)
	}
	c.send(wsMessage{ID: "1", Type: "subscribe", Payload: payload})
}

func TestChatUpdatesSubscription(t *testing.T) {
	f := newOrgFixture()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv := httptest.NewServer(as(&f.olivia, NewHandler("v1", "https://console.example.com", nil, f.service, f.repos, f.code, f.chats)))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	if conn, err := dialWS(ctx, url, "https://elsewhere.example.com"); err == nil {
		_ = conn.CloseNow() // the test failed already; closing it is tidiness
		t.Fatal("a page from another origin opened a WebSocket")
	} else if !strings.Contains(err.Error(), "status 403") {
		t.Fatalf("a page from another origin: %v, want 403", err)
	}
	conn, err := dialWS(ctx, url, "https://console.example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }() // the test is done with it
	c := &wsClient{t: t, ctx: ctx, conn: conn}
	c.subscribe(`subscription { chatUpdates(repositoryId: "` + f.website.ID.String() + `", branch: "feature/login", after: "0") { cursor kind thread { id } message { body } } }`)
	if body := f.query(&f.olivia, `mutation { startChatThread(repositoryId: "`+f.website.ID.String()+`", branch: "feature/login", input: {body: "Hello"}) { id } }`); strings.Contains(body, `"errors"`) {
		t.Fatal(body)
	}
	subj, err := f.chatStore.Subject(ctx, f.website.ID, "feature/login")
	if err != nil {
		t.Fatal(err)
	}
	f.notifier.Wake(subj.BranchID)
	if m := c.read(); m.Type != "next" || m.ID != "1" || !strings.Contains(string(m.Payload), `"cursor":"1","kind":"THREAD_STARTED"`) ||
		!strings.Contains(string(m.Payload), `"message":{"body":"Hello"}`) {
		t.Fatalf("update = %s %s, want the thread started", m.Type, m.Payload)
	}
}
