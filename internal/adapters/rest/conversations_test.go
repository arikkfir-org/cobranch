package rest

import (
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

// addConversations lets the fixture's chats hold conversations about Acme, about website and with Scout.
func (f *orgFixture) addConversations() {
	f.chatStore.SetOrganization(f.acme.ID, "acme")
	f.chatStore.SetRepository(f.acme.ID, f.website.ID, "website", shaOf("c"))
	f.chatStore.SetAgent(f.acme.ID, f.scout.ID, f.scout.Name)
}

func TestConversations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// path is the conversation's path, of the agent's ID.
		path func(f *orgFixture) string
		// max is Max, holding what reads the conversation's subject.
		max func(f *orgFixture) *auth.Authenticated
		// outsider is who may not see the subject, and the refusal's code.
		outsider func(f *orgFixture) *auth.Authenticated
		gone     string
	}{
		{name: "organization", path: fixed("/v1/orgs/acme/conversation"), max: maxim, outsider: nia, gone: "organization.not_found"},
		{name: "repository", path: fixed("/v1/orgs/acme/repos/website/conversation"), max: maxAs(auth.RoleReader), outsider: niaGuest,
			gone: "repository.not_found"},
		{name: "agent", path: func(f *orgFixture) string { return "/v1/orgs/acme/agents/" + f.scout.ID.String() + "/conversation" }, max: maxim,
			outsider: nia, gone: "organization.not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newOrgFixture(t)
			f.addConversations()
			chat := tt.path(f)
			anchor := `"anchor":{"sha":"` + shaOf("c") + `","path":"README.md","startLine":1,"endLine":2}`
			steps := []chatStep{
				{name: "Max starts a thread", viewer: tt.max, method: "POST", path: "/threads", body: `{"body":"Where does the push worker start?"}`,
					wantStatus: 201, want: []string{`"startedBy":{"displayName":"Max"`}, wantTag: `"1-`,
					wantHeader: map[string]string{"Location": chat + "/threads/{thread}"}},
				{name: "a conversation's thread points at no file", viewer: tt.max, method: "POST", path: "/threads", body: `{"body":"Here",` + anchor + `}`,
					wantStatus: 400, wantCode: "anchor.not_allowed"},
				{name: "Max follows up", viewer: tt.max, method: "POST", path: "/threads/{thread}/messages", body: `{"body":"In cmd/cobranch?"}`,
					wantStatus: 201, want: []string{`"author":{"displayName":"Max"`}, wantTag: `"1-`,
					wantHeader: map[string]string{"Location": chat + "/threads/{thread}/messages/{message}"}},
				{name: "Max reads his conversation", viewer: tt.max, method: "GET", path: "", wantStatus: 200,
					want: []string{`"after":"2"`, `"id":"{thread}"`, `"body":"In cmd/cobranch?"`}},
				{name: "the Owner's own conversation holds none of it", viewer: olivia, method: "GET", path: "", wantStatus: 200,
					want: []string{`"threads":[]`, `"after":"0"`}},
				{name: "the Owner finds no thread of Max's", viewer: olivia, method: "GET", path: "/threads/{thread}", wantStatus: 404, wantCode: "thread.not_found"},
				{name: "the Owner may not post to Max's thread", viewer: olivia, method: "POST", path: "/threads/{thread}/messages", body: `{"body":"Hi"}`,
					wantStatus: 404, wantCode: "thread.not_found"},
				{name: "the Owner may not delete Max's message", viewer: olivia, method: "DELETE", path: "/threads/{thread}/messages/{message}", ifMatch: "*",
					wantStatus: 404},
				{name: "the Owner follows none of Max's updates", viewer: olivia, method: "GET", path: "/updates?after=0", wantStatus: 200,
					want: []string{`"updates":[]`}},
				{name: "Max edits his message", viewer: tt.max, method: "PATCH", path: "/threads/{thread}/messages/{message}", body: `{"body":"In cmd/cobranch."}`,
					ifMatch: "{tag}", wantStatus: 200, want: []string{`"editedAt":`}, wantTag: `"2-`},
				{name: "the message, still the one held", viewer: tt.max, method: "GET", path: "/threads/{thread}/messages/{message}", ifNoneMatch: "{tag}",
					wantStatus: 304, wantTag: `"2-`},
				{name: "Max resolves his thread", viewer: tt.max, method: "PATCH", path: "/threads/{thread}", body: `{"resolved":true}`, ifMatch: "*",
					wantStatus: 200, want: []string{`"resolvedBy":{"displayName":"Max"`}, wantTag: `"4-`},
				{name: "the updates, in order", viewer: tt.max, method: "GET", path: "/updates?after=0", wantStatus: 200,
					want: []string{`"after":"4"`, `"kind":"thread.started"`, `"kind":"message.posted"`, `"kind":"message.edited"`, `"kind":"thread.resolved"`}},
				{name: "Max deletes his message", viewer: tt.max, method: "DELETE", path: "/threads/{thread}/messages/{message}", ifMatch: "*", wantStatus: 204},
				{name: "no such thread", viewer: tt.max, method: "GET", path: "/threads/" + uuid.NewString(), wantStatus: 404, wantCode: "thread.not_found"},
				{name: "an outsider finds none", viewer: tt.outsider, method: "GET", path: "", wantStatus: 404, wantCode: tt.gone},
			}
			var ids chatIDs
			for _, s := range steps {
				s.run(t, f, chat, &ids)
			}
		})
	}
}

func TestConversationsNotFound(t *testing.T) {
	t.Parallel()
	f := newOrgFixture(t)
	f.addConversations()
	tests := map[string]string{
		"/v1/orgs/acme/repos/blog/conversation":                            "repository.not_found",
		"/v1/orgs/acme/agents/" + uuid.NewString() + "/conversation":       "agent.not_found",
		"/v1/orgs/acme/agents/" + f.max.User.ID.String() + "/conversation": "agent.not_found",
		"/v1/orgs/nowhere/conversation":                                    "organization.not_found",
	}
	for path, code := range tests {
		for _, p := range []string{path, path + "/updates?after=0"} {
			if rec := f.call(&f.olivia, "GET", p, ""); rec.Code != 404 {
				t.Errorf("GET %s = %d, want 404: %s", p, rec.Code, rec.Body)
			} else {
				checkBody(t, rec, "", code)
			}
		}
	}
}
