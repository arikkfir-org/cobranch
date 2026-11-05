package rest

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents/agentstest"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/chats/chatstest"
)

// addAsking lets the fixture's chats ask its agents, with the conversations: Scout, a Member of Acme and a Developer
// on website, reads every chat.
func (f *orgFixture) addAsking(t *testing.T) {
	t.Helper()
	f.addAgents(t)
	f.addConversations()
	feature, err := f.chatStore.Subject(t.Context(), f.website.ID, "feature/login")
	if err != nil {
		t.Fatal(err)
	}
	f.agentStore.Branches[feature.BranchID] = agentstest.Branch{Repo: f.website.ID, Name: "feature/login"}
	f.store.AddMember(f.acme.ID, f.scout.ID, auth.RoleMember)
	f.store.Grant(f.scout.ID, auth.RoleDeveloper, auth.ScopeRepository, f.website.ID)
	f.chatStore.Names[f.scout.ID] = "Scout"
	f.chats.WithAgents(f.agents, chatstest.AgentTasks{Store: f.agentStore, Chats: f.chatStore})
}

// TestAsk has Max, a Developer on website, ask Scout in feature/login's chat, where the thread's task holds it until it
// ends, and in his conversation with Scout, which asks Scout by default for a task private to him: the task list shows
// it whole to Max and as a place in Scout's queue to Olivia.
func TestAsk(t *testing.T) {
	t.Parallel()
	f := newOrgFixture(t)
	f.addAsking(t)
	scout := f.scout.ID.String()
	const branch = "/v1/orgs/acme/repos/website/branches/feature%2Flogin/chat"
	steps := []chatStep{
		{name: "Max asks Scout in a new thread", viewer: maxAs(auth.RoleDeveloper), method: "POST", path: "/threads",
			body: `{"body":"Have a look at the login","ask":"` + scout + `"}`, wantStatus: 201,
			want: []string{`"askedTaskNumber":4`, `"askedAgent":{"displayName":"Scout"`, `"askedTaskId":"`}},
		{name: "the thread's task still runs", viewer: maxAs(auth.RoleDeveloper), method: "POST", path: "/threads/{thread}/messages",
			body: `{"body":"And the signup","ask":"` + scout + `"}`, wantStatus: 400, wantCode: "ask.busy"},
		{name: "no such agent", viewer: maxAs(auth.RoleDeveloper), method: "POST", path: "/threads",
			body: `{"body":"Anyone?","ask":"` + uuid.NewString() + `"}`, wantStatus: 400, wantCode: "ask.invalid"},
		{name: "a reply that asks nobody", viewer: maxAs(auth.RoleDeveloper), method: "POST", path: "/threads/{thread}/messages",
			body: `{"body":"Thanks"}`, wantStatus: 201, want: []string{`"body":"Thanks"`}},
	}
	var ids chatIDs
	for _, s := range steps {
		s.run(t, f, branch, &ids)
	}
	if strings.Contains(f.call(&f.max, "GET", branch+"/threads/"+ids.thread+"/messages/"+ids.message, "").Body.String(), `"askedTaskNumber"`) {
		t.Error("a reply that asks nobody names a task")
	}

	conversation := "/v1/orgs/acme/agents/" + scout + "/conversation"
	(chatStep{name: "Max talks with Scout", viewer: maxim, method: "POST", path: "/threads", body: `{"body":"Which tasks are waiting?"}`,
		wantStatus: 201, want: []string{`"askedTaskNumber":5`, `"askedAgent":{"displayName":"Scout"`}}).run(t, f, conversation, &ids)
	mine := f.call(&f.max, "GET", "/v1/orgs/acme/tasks/5", "")
	if mine.Code != 200 || !strings.Contains(mine.Body.String(), `"private":true`) || !strings.Contains(mine.Body.String(), `"title":"Which tasks are waiting?"`) {
		t.Errorf("Max's task 5 = %d %s; want it whole, private", mine.Code, mine.Body)
	}
	if rec := f.call(&f.olivia, "GET", "/v1/orgs/acme/tasks/5", ""); rec.Code != 404 {
		t.Errorf("Olivia's GET of Max's private task = %d %s; want 404", rec.Code, rec.Body)
	}
	listed := f.call(&f.olivia, "GET", "/v1/orgs/acme/tasks?agent="+scout, "").Body.String()
	if !strings.Contains(listed, `"number":5`) || strings.Contains(listed, "Which tasks are waiting?") || !strings.Contains(listed, `"private":true`) {
		t.Errorf("Olivia's list of Scout's tasks = %s; want task 5 redacted", listed)
	}
}
