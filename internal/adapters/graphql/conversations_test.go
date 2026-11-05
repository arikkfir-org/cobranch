package graphql

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/chats"
)

// addConversations lets the fixture's chats hold conversations about Acme, about website and with Scout.
func (f *orgFixture) addConversations() {
	f.chatStore.SetOrganization(f.acme.ID, "acme")
	f.chatStore.SetRepository(f.acme.ID, f.website.ID, "website", shaOf("c"))
	f.chatStore.SetAgent(f.acme.ID, f.scout.ID, f.scout.Name)
}

func TestConversations(t *testing.T) {
	t.Parallel()
	const page = `conversation { after nextCursor threads { id anchor { path } resolvedBy { displayName } messages { body deletedBy { displayName } } } }`
	tests := []struct {
		name string
		// subject names the conversation in a change, and read reads it, of the agent's ID.
		subject, read string
		max           func(f *orgFixture) *auth.Authenticated
		gone          string
	}{
		{name: "organization", subject: `subject: {organizationId: "{org}"}`, read: `{ organization(slug: "acme") { ` + page + ` } }`, max: maxim,
			gone: "organization.not_found"},
		{name: "repository", subject: `subject: {organizationId: "{org}", repositoryId: "{repo}"}`,
			read: `{ organization(slug: "acme") { repository(slug: "website") { ` + page + ` } } }`, max: maxAs(auth.RoleReader), gone: "repository.not_found"},
		{name: "agent", subject: `subject: {organizationId: "{org}", agentId: "{agent}"}`,
			read: `{ organization(slug: "acme") { agent(id: "{agent}") { ` + page + ` } } }`, max: maxim, gone: "agent.not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newOrgFixture()
			f.addConversations()
			agent := strings.NewReplacer("{agent}", f.scout.ID.String())
			target, read := agent.Replace(tt.subject), agent.Replace(tt.read)
			steps := []chatStep{
				{name: "Max starts a thread", viewer: tt.max,
					document: `mutation { startConversationThread(` + target + `, input: {body: "Where does the push worker start?"}) { id anchor { path } startedBy { displayName } } }`,
					want:     []string{`"anchor":null,"startedBy":{"displayName":"Max"}`}},
				{name: "a conversation's thread points at no file", viewer: tt.max,
					document: `mutation { startConversationThread(` + target + `, input: {body: "Here", anchor: {sha: "` + shaOf("c") + `", path: "a", startLine: 1, endLine: 1}}) { id } }`,
					wantCode: badUserInput, wantReason: "anchor.not_allowed"},
				{name: "Max follows up", viewer: tt.max,
					document: `mutation { postConversationMessage(` + target + `, thread: "{thread}", input: {body: "In cmd/cobranch?"}) { id threadId author { displayName } } }`,
					want:     []string{`"threadId":"{thread}","author":{"displayName":"Max"}`}},
				{name: "Max reads his conversation", viewer: tt.max, document: read,
					want: []string{`"after":"2","nextCursor":null`, `"id":"{thread}","anchor":null`, `{"body":"In cmd/cobranch?","deletedBy":null}`}},
				{name: "the Owner's own conversation holds none of it", viewer: olivia, document: read, want: []string{`"after":"0","nextCursor":null,"threads":[]`}},
				{name: "the Owner may not post to Max's thread", viewer: olivia,
					document: `mutation { postConversationMessage(` + target + `, thread: "{thread}", input: {body: "Hi"}) { id } }`,
					wantCode: notFound, wantReason: "thread.not_found"},
				{name: "Max edits his message", viewer: tt.max,
					document: `mutation { editConversationMessage(` + target + `, thread: "{thread}", message: "{message}", body: "In cmd/cobranch.", expectedVersion: 1) { body version } }`,
					want:     []string{`"body":"In cmd/cobranch.","version":2`}},
				{name: "Max resolves his thread", viewer: tt.max,
					document: `mutation { resolveConversationThread(` + target + `, thread: "{thread}", resolved: true) { resolvedBy { displayName } } }`,
					want:     []string{`"resolvedBy":{"displayName":"Max"}`}},
				{name: "Max deletes his message", viewer: tt.max,
					document: `mutation { deleteConversationMessage(` + target + `, thread: "{thread}", message: "{message}") }`,
					want:     []string{`{"data":{"deleteConversationMessage":true}}`}},
				{name: "an outsider finds none", viewer: nia,
					document: `mutation { startConversationThread(` + target + `, input: {body: "Hi"}) { id } }`, wantCode: notFound, wantReason: tt.gone},
			}
			var ids chatIDs
			for _, s := range steps {
				s.run(t, f, &ids)
			}
		})
	}
}

func TestConversationSubjects(t *testing.T) {
	t.Parallel()
	f := newOrgFixture()
	f.addConversations()
	tests := []struct {
		name, subject        string
		wantCode, wantReason string
	}{
		{name: "both a repository and an agent", subject: `{organizationId: "{org}", repositoryId: "{repo}", agentId: "{agent}"}`, wantCode: badUserInput,
			wantReason: "conversation.invalid"},
		{name: "no such repository", subject: `{organizationId: "{org}", repositoryId: "` + f.acme.ID.String() + `"}`, wantCode: notFound,
			wantReason: "repository.not_found"},
		{name: "a person as an agent", subject: `{organizationId: "{org}", agentId: "` + f.max.User.ID.String() + `"}`, wantCode: notFound,
			wantReason: "agent.not_found"},
		{name: "no ID", subject: `{organizationId: "acme"}`, wantCode: notFound, wantReason: "organization.not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subject := strings.NewReplacer("{agent}", f.scout.ID.String()).Replace(tt.subject)
			s := chatStep{name: tt.name, viewer: olivia, document: `mutation { startConversationThread(subject: ` + subject + `, input: {body: "Hi"}) { id } }`,
				wantCode: tt.wantCode, wantReason: tt.wantReason}
			s.run(t, f, &chatIDs{})
		})
	}
}

func TestConversationUpdatesSubscription(t *testing.T) {
	t.Parallel()
	f := newOrgFixture()
	f.addConversations()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv := httptest.NewServer(as(&f.olivia, NewHandler("v1", "https://console.example.com", nil, f.service, f.repos, f.code, f.issues, f.chats, f.agents, f.secrets, f.artifacts)))
	defer srv.Close()
	conn, err := dialWS(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), "https://console.example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }() // the test is done with it
	c := &wsClient{t: t, ctx: ctx, conn: conn}
	subject := `subject: {organizationId: "` + f.acme.ID.String() + `", agentId: "` + f.scout.ID.String() + `"}`
	c.subscribe(`subscription { conversationUpdates(` + subject + `, after: "0") { cursor kind message { body } } }`)
	if body := f.query(&f.olivia, `mutation { startConversationThread(`+subject+`, input: {body: "Hello"}) { id } }`); strings.Contains(body, `"errors"`) {
		t.Fatal(body)
	}
	f.notifier.Wake(chats.ConversationID(f.olivia.User.ID, f.acme.ID, uuid.Nil, f.scout.ID))
	if m := c.read(); m.Type != "next" || !strings.Contains(string(m.Payload), `"cursor":"1","kind":"THREAD_STARTED","message":{"body":"Hello"}`) {
		t.Fatalf("update = %s %s, want the thread started", m.Type, m.Payload)
	}
}
