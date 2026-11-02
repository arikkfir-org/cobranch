package rest

import (
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

// niaGuest is Nia, made a guest of Acme, who sees it without reading its issues.
func niaGuest(f *orgFixture) *auth.Authenticated {
	f.store.AddMember(f.acme.ID, f.nia.User.ID, auth.RoleGuest)
	return &f.nia
}

func TestIssueChats(t *testing.T) {
	const chat = "/v1/orgs/acme/issues/7/chat"
	anchor := `"anchor":{"sha":"` + shaOf("c") + `","path":"README.md","startLine":1,"endLine":2}`
	steps := []chatStep{
		{name: "a Member comments", viewer: maxim, method: "POST", path: "/threads", body: `{"body":"See [the guide](docs/guide.md)"}`,
			wantStatus: 201, want: []string{`"startedBy":{"displayName":"Max"`, `/acme/issues/docs/guide.md`}, wantTag: `"1-`,
			wantHeader: map[string]string{"Location": chat + "/threads/{thread}"}},
		{name: "a comment points at no file", viewer: maxim, method: "POST", path: "/threads", body: `{"body":"Here",` + anchor + `}`,
			wantStatus: 400, wantCode: "anchor.not_allowed"},
		{name: "an Owner replies", viewer: olivia, method: "POST", path: "/threads/{thread}/messages", body: `{"body":"On it"}`,
			wantStatus: 201, want: []string{`"author":{"displayName":"Olivia"`}, wantTag: `"1-`,
			wantHeader: map[string]string{"Location": chat + "/threads/{thread}/messages/{message}"}},
		{name: "a Member reads the comments", viewer: maxim, method: "GET", path: "", wantStatus: 200,
			want: []string{`"after":"2"`, `"id":"{thread}"`, `"body":"On it"`}},
		{name: "a Member may not delete the Owner's reply", viewer: maxim, method: "DELETE", path: "/threads/{thread}/messages/{message}", ifMatch: "*",
			wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "the Owner edits her reply", viewer: olivia, method: "PATCH", path: "/threads/{thread}/messages/{message}", body: `{"body":"On it, today"}`,
			ifMatch: "{tag}", wantStatus: 200, want: []string{`"editedAt":`}, wantTag: `"2-`},
		{name: "the reply, read", viewer: maxim, method: "GET", path: "/threads/{thread}/messages/{message}", wantStatus: 200, wantTag: `"2-`},
		{name: "the reply, still the one held", viewer: maxim, method: "GET", path: "/threads/{thread}/messages/{message}", ifNoneMatch: "{tag}",
			wantStatus: 304, wantTag: `"2-`},
		{name: "the Owner resolves the Member's thread", viewer: olivia, method: "PATCH", path: "/threads/{thread}", body: `{"resolved":true}`, ifMatch: "*",
			wantStatus: 200, want: []string{`"resolvedBy":{"displayName":"Olivia"`}, wantTag: `"4-`},
		{name: "the thread, still the one held", viewer: maxim, method: "GET", path: "/threads/{thread}", ifNoneMatch: "{tag}",
			wantStatus: 304, wantTag: `"4-`},
		{name: "the updates, in order", viewer: maxim, method: "GET", path: "/updates?after=0", wantStatus: 200,
			want: []string{`"after":"4"`, `"kind":"thread.started"`, `"kind":"message.posted"`, `"kind":"message.edited"`, `"kind":"thread.resolved"`}},
		{name: "the Owner deletes the Member's comment", viewer: olivia, method: "DELETE", path: "/threads/{thread}/messages/{message}", ifMatch: "*",
			wantStatus: 204},
		{name: "no such thread", viewer: maxim, method: "GET", path: "/threads/" + uuid.NewString(), wantStatus: 404, wantCode: "thread.not_found"},
		{name: "an outsider finds no organization", viewer: nia, method: "GET", path: "", wantStatus: 404, wantCode: "organization.not_found"},
		{name: "a Guest may not read the comments", viewer: niaGuest, method: "GET", path: "", wantStatus: 403, wantCode: "auth.forbidden"},
	}
	f := newOrgFixture(t)
	var ids chatIDs
	for _, s := range steps {
		s.run(t, f, chat, &ids)
	}
	for _, path := range []string{"/v1/orgs/acme/issues/8/chat", "/v1/orgs/acme/issues/8/chat/updates?after=0"} {
		if rec := f.call(&f.max, "GET", path, ""); rec.Code != 404 {
			t.Errorf("GET %s = %d, want 404: %s", path, rec.Code, rec.Body)
		} else {
			checkBody(t, rec, "", "issue.not_found")
		}
	}
}
