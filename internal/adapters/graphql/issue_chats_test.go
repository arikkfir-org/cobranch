package graphql

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// withIssue creates Acme's first issue, ACME-1, whose chat the fixture's chats' store knows.
func withIssue(t *testing.T, f *orgFixture) {
	t.Helper()
	if body := f.query(&f.olivia, `mutation { createIssue(organizationId: "`+f.acme.ID.String()+`", input: {title: "Safari"}) { identifier } }`); !strings.Contains(body, `"ACME-1"`) {
		t.Fatal(body)
	}
}

func TestIssueChats(t *testing.T) {
	const target = `organizationId: "{org}", number: 1`
	const chat = `{ organization(slug: "acme") { issue(number: 1) { chat { after nextCursor threads { id anchor { path } resolvedBy { displayName } messages { body deletedBy { displayName } } } } } } }`
	steps := []chatStep{
		{name: "a Member comments", viewer: maxim,
			document: `mutation { startIssueChatThread(` + target + `, input: {body: "Seen in *Safari* too"}) { id anchor { path } startedBy { displayName } messages { html } } }`,
			want:     []string{`"anchor":null,"startedBy":{"displayName":"Max"}`, `\u003cem\u003eSafari\u003c/em\u003e`}},
		{name: "a comment points at no file", viewer: maxim,
			document: `mutation { startIssueChatThread(` + target + `, input: {body: "Here", anchor: {sha: "` + shaOf("c") + `", path: "a", startLine: 1, endLine: 1}}) { id } }`,
			wantCode: badUserInput, wantReason: "anchor.not_allowed"},
		{name: "an Owner replies", viewer: olivia,
			document: `mutation { postIssueChatMessage(` + target + `, thread: "{thread}", input: {body: "On it"}) { id threadId author { displayName } } }`,
			want:     []string{`"threadId":"{thread}","author":{"displayName":"Olivia"}`}},
		{name: "a Member reads the comments", viewer: maxim, document: chat,
			want: []string{`"after":"2","nextCursor":null`, `"id":"{thread}","anchor":null`, `{"body":"On it","deletedBy":null}`}},
		{name: "a Member may not delete the Owner's reply", viewer: maxim,
			document: `mutation { deleteIssueChatMessage(` + target + `, thread: "{thread}", message: "{message}") }`, wantCode: forbidden},
		{name: "the Owner edits her reply", viewer: olivia,
			document: `mutation { editIssueChatMessage(` + target + `, thread: "{thread}", message: "{message}", body: "On it, today", expectedVersion: 1) { body version } }`,
			want:     []string{`"body":"On it, today","version":2`}},
		{name: "the Owner resolves the Member's thread", viewer: olivia,
			document: `mutation { resolveIssueChatThread(` + target + `, thread: "{thread}", resolved: true) { resolvedBy { displayName } } }`,
			want:     []string{`"resolvedBy":{"displayName":"Olivia"}`}},
		{name: "the Owner deletes her reply", viewer: olivia,
			document: `mutation { deleteIssueChatMessage(` + target + `, thread: "{thread}", message: "{message}") }`, want: []string{`{"data":{"deleteIssueChatMessage":true}}`}},
		{name: "an outsider finds no issue", viewer: nia,
			document: `mutation { startIssueChatThread(` + target + `, input: {body: "Hi"}) { id } }`, wantCode: notFound, wantReason: "issue.not_found"},
		{name: "no such issue", viewer: maxim,
			document: `mutation { startIssueChatThread(organizationId: "{org}", number: 2, input: {body: "Hi"}) { id } }`, wantCode: notFound, wantReason: "issue.not_found"},
	}
	f := newOrgFixture()
	withIssue(t, f)
	var ids chatIDs
	for _, s := range steps {
		s.run(t, f, &ids)
	}
}

func TestIssueChatUpdatesSubscription(t *testing.T) {
	f := newOrgFixture()
	withIssue(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv := httptest.NewServer(as(&f.olivia, NewHandler("v1", "https://console.example.com", nil, f.service, f.repos, f.code, f.issues, f.chats, f.agents)))
	defer srv.Close()
	conn, err := dialWS(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), "https://console.example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }() // the test is done with it
	c := &wsClient{t: t, ctx: ctx, conn: conn}
	org := f.acme.ID.String()
	c.subscribe(`subscription { issueChatUpdates(organizationId: "` + org + `", number: 1, after: "0") { cursor kind message { body } } }`)
	if body := f.query(&f.olivia, `mutation { startIssueChatThread(organizationId: "`+org+`", number: 1, input: {body: "Hello"}) { id } }`); strings.Contains(body, `"errors"`) {
		t.Fatal(body)
	}
	subj, err := f.chatStore.Issue(ctx, f.acme.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.notifier.Wake(subj.IssueID)
	if m := c.read(); m.Type != "next" || !strings.Contains(string(m.Payload), `"cursor":"1","kind":"THREAD_STARTED","message":{"body":"Hello"}`) {
		t.Fatalf("update = %s %s, want the thread started", m.Type, m.Payload)
	}
}
