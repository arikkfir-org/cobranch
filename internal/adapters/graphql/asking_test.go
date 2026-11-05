package graphql

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

// TestAsk has Max ask Scout in feature/login's chat, and talk with Scout in his conversation, which asks Scout by
// default for a task private to him: Olivia sees that task as a place in Scout's queue alone.
func TestAsk(t *testing.T) {
	t.Parallel()
	f := newOrgFixture()
	f.addAsking(t)
	scout := f.scout.ID.String()
	const branch = `repositoryId: "{repo}", branch: "feature/login"`
	conversation := `subject: {organizationId: "{org}", agentId: "` + scout + `"}`
	asked := `askedTaskId askedTaskNumber askedAgent { displayName }`
	steps := []chatStep{
		{name: "Max asks Scout in a new thread", viewer: maxAs(auth.RoleDeveloper),
			document: `mutation { startChatThread(` + branch + `, input: {body: "Have a look at the login", ask: "` + scout + `"}) { id messages { ` + asked + ` } } }`,
			want:     []string{`"askedTaskNumber":4,"askedAgent":{"displayName":"Scout"}`}},
		{name: "the thread's task still runs", viewer: maxAs(auth.RoleDeveloper),
			document: `mutation { postChatMessage(` + branch + `, thread: "{thread}", input: {body: "And the signup", ask: "` + scout + `"}) { id } }`,
			wantCode: badUserInput, wantReason: "ask.busy"},
		{name: "no such agent", viewer: maxAs(auth.RoleDeveloper),
			document: `mutation { startChatThread(` + branch + `, input: {body: "Anyone?", ask: "` + uuid.NewString() + `"}) { id } }`,
			wantCode: badUserInput, wantReason: "ask.invalid"},
		{name: "not an ID", viewer: maxAs(auth.RoleDeveloper),
			document: `mutation { startChatThread(` + branch + `, input: {body: "Anyone?", ask: "scout"}) { id } }`,
			wantCode: badUserInput, wantReason: "ask.invalid"},
		{name: "a reply that asks nobody", viewer: maxAs(auth.RoleDeveloper),
			document: `mutation { postChatMessage(` + branch + `, thread: "{thread}", input: {body: "Thanks"}) { id ` + asked + ` } }`,
			want:     []string{`"askedTaskId":null,"askedTaskNumber":null,"askedAgent":null`}},
		{name: "Max talks with Scout", viewer: maxim,
			document: `mutation { startConversationThread(` + conversation + `, input: {body: "Which tasks are waiting?"}) { id messages { ` + asked + ` } } }`,
			want:     []string{`"askedTaskNumber":5,"askedAgent":{"displayName":"Scout"}`}},
	}
	var ids chatIDs
	for _, s := range steps {
		s.run(t, f, &ids)
	}
	tasks := `{ organization(slug: "acme") { tasks(agent: "` + scout + `") { tasks { number private title } } } }`
	if body := f.query(&f.max, tasks); !strings.Contains(body, `{"number":5,"private":true,"title":"Which tasks are waiting?"}`) {
		t.Errorf("Max's tasks = %s; want task 5 whole", body)
	}
	if body := f.query(&f.olivia, tasks); !strings.Contains(body, `{"number":5,"private":true,"title":""}`) {
		t.Errorf("Olivia's tasks = %s; want task 5 redacted", body)
	}
}
