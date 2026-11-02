package graphql

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/agents/agentstest"
	"cobranch.dev/internal/services/auth"
)

// addAgents gives the fixture the agents' service, which knows the providers anthropic and openai, with Olivia's agent
// Scout and the built-in Reviewer, Scout's tasks: 1 by Max anywhere, 2 by Olivia on website's feature/login, which Max
// doesn't read, and 3 by Max, succeeded; and the secrets providerKey of Acme's and deployKey of website's.
func (f *orgFixture) addAgents() {
	f.agentStore = agentstest.NewStore()
	f.agentStore.Now = now
	for _, v := range []auth.Authenticated{f.olivia, f.max, f.nia} {
		f.agentStore.Names[v.User.ID] = v.User.DisplayName
	}
	f.agentStore.Repos[f.website.ID] = agentstest.Repository{Org: f.acme.ID, Slug: "website"}
	f.agentStore.Branches[uuid.New()] = agentstest.Branch{Repo: f.website.ID, Name: "feature/login"}
	f.agents = agents.NewService(f.agentStore, auth.NewAuthorizer(f.store)).WithProviders("anthropic", "openai")
	f.providerKey, f.deployKey = f.agentStore.AddSecret(f.acme.ID, uuid.Nil, "anthropic-key"), f.agentStore.AddSecret(f.acme.ID, f.website.ID, "deploy-key")
	f.scout = f.agentStore.AddAgent(agents.Agent{OrgID: f.acme.ID, Name: "Scout", CreatedBy: &agents.Principal{ID: f.olivia.User.ID, DisplayName: "Olivia"}})
	f.agentStore.AddAgent(agents.Agent{OrgID: f.acme.ID, Name: "Reviewer", BuiltIn: true})
	for _, a := range []struct {
		by *auth.Authenticated
		in agents.TaskInput
	}{
		{&f.max, agents.TaskInput{Title: "Tidy up"}},
		{&f.olivia, agents.TaskInput{Title: "Fix the login", RepoID: f.website.ID, Branch: "feature/login"}},
		{&f.max, agents.TaskInput{Title: "Done already"}},
	} {
		if _, err := f.agents.Assign(context.Background(), a.by.User, f.acme.ID, f.scout.ID, a.in); err != nil {
			panic(err)
		}
	}
	f.agentStore.SetState(f.acme.ID, 3, agents.TaskSucceeded)
}

// ofAcme is a query of acme's fields.
func ofAcme(fields string) func(*orgFixture) string {
	return fixed(`{ organization(slug: "acme") { ` + fields + ` } }`)
}

// withScout is a document that names Acme's ID where it says ACME, Scout's where it says SCOUT and website's where it
// says WEBSITE.
func withScout(document string) func(*orgFixture) string {
	return func(f *orgFixture) string {
		return strings.NewReplacer("ACME", f.acme.ID.String(), "SCOUT", f.scout.ID.String(), "WEBSITE", f.website.ID.String()).Replace(document)
	}
}

func TestAgentQueries(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "agents", viewer: maxim, document: ofAcme(`agents { name builtIn createdBy { displayName } version grants { roleName } }`),
			want: []string{`"agents":[{"name":"Reviewer","builtIn":true,"createdBy":null,"version":1,"grants":[]},{"name":"Scout","builtIn":false,"createdBy":{"displayName":"Olivia"}`}},
		{name: "an agent", viewer: maxim, document: withScout(`{ organization(slug: "acme") { agent(id: "SCOUT") { name } } }`), want: []string{`"agent":{"name":"Scout"}`}},
		{name: "an agent not there", viewer: maxim, document: ofAcme(`agent(id: "` + uuid.NewString() + `") { name }`), want: []string{`"agent":null`}},
		{name: "an agent of no ID", viewer: maxim, document: ofAcme(`agent(id: "x") { name }`), want: []string{`"agent":null`}},
		{name: "tasks", viewer: olivia, document: ofAcme(`tasks { tasks { number state agent { displayName } } nextCursor }`),
			want: []string{`"tasks":[{"number":3,"state":"SUCCEEDED","agent":{"displayName":"Scout"}},{"number":2,`, `{"number":1,`, `"nextCursor":null`}},
		{name: "tasks on repositories the viewer reads", viewer: maxim, document: ofAcme(`tasks { tasks { number } }`),
			want: []string{`"tasks":[{"number":3},{"number":1}]`}},
		{name: "a page of active tasks", viewer: olivia, document: ofAcme(`tasks(states: [QUEUED, RUNNING], first: 1) { tasks { number } nextCursor }`),
			want: []string{`"tasks":[{"number":2}],"nextCursor":"2"`}},
		{name: "the next page", viewer: olivia, document: ofAcme(`tasks(states: [QUEUED], first: 1, after: "2") { tasks { number } nextCursor }`),
			want: []string{`"tasks":[{"number":1}],"nextCursor":null`}},
		{name: "a repository's tasks", viewer: olivia,
			document: withScout(`{ organization(slug: "acme") { tasks(repositoryId: "WEBSITE", agent: "SCOUT") { tasks { number branch repositorySlug } } } }`),
			want:     []string{`"tasks":[{"number":2,"branch":"feature/login","repositorySlug":"website"}]`}},
		{name: "tasks from a bad cursor", viewer: olivia, document: ofAcme(`tasks(after: "x") { tasks { number } }`), wantCode: "BAD_USER_INPUT", wantReason: "cursor.invalid"},
		{name: "tasks past a page", viewer: olivia, document: ofAcme(`tasks(first: 101) { tasks { number } }`), wantCode: "BAD_USER_INPUT", wantReason: "limit.invalid"},
		{name: "a task", viewer: olivia, document: ofAcme(`task(number: 2) { title assignedBy { displayName } startedAt version }`),
			want: []string{`"task":{"title":"Fix the login","assignedBy":{"displayName":"Olivia"},"startedAt":null,"version":1}`}},
		{name: "a task on a repository the viewer doesn't read", viewer: maxim, document: ofAcme(`task(number: 2) { title }`), want: []string{`"task":null`}},
	})
}

func TestAgentMutations(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "create", viewer: maxim,
			document: withScout(`mutation { createAgent(organizationId: "ACME", input: {name: "Helper", grants: [{roleId: "` + auth.RoleMember.String() + `"}]}) { name version grants { roleName repositoryId } } }`),
			want:     []string{`"createAgent":{"name":"Helper","version":1,"grants":[{"roleName":"Member","repositoryId":null}]}`}},
		{name: "create with a name taken in another case", viewer: maxim, document: withScout(`mutation { createAgent(organizationId: "ACME", input: {name: "scout"}) { name } }`),
			wantCode: "CONFLICT", wantReason: "agent.name_taken"},
		{name: "create with a role beyond the creator's", viewer: maxim,
			document: withScout(`mutation { createAgent(organizationId: "ACME", input: {name: "Root", grants: [{roleId: "` + auth.RoleOwner.String() + `"}]}) { name } }`),
			wantCode: "FORBIDDEN"},
		{name: "create with a role of no ID", viewer: maxim, document: withScout(`mutation { createAgent(organizationId: "ACME", input: {name: "X", grants: [{roleId: "x"}]}) { name } }`),
			wantCode: "BAD_USER_INPUT", wantReason: "role.invalid"},
		{name: "create as an outsider", viewer: nia, document: withScout(`mutation { createAgent(organizationId: "ACME", input: {name: "Mine"}) { name } }`),
			wantCode: "NOT_FOUND", wantReason: "organization.not_found"},
		{name: "update", viewer: olivia, document: withScout(`mutation { updateAgent(organizationId: "ACME", id: "SCOUT", input: {instructions: "Look first."}, expectedVersion: 1) { instructions version } }`),
			want: []string{`"updateAgent":{"instructions":"Look first.","version":2}`}},
		{name: "update from a stale version", viewer: olivia, document: withScout(`mutation { updateAgent(organizationId: "ACME", id: "SCOUT", input: {name: "Late"}, expectedVersion: 2) { name } }`),
			wantCode: "CONFLICT", wantReason: "precondition.failed"},
		{name: "update another's agent", viewer: maxim, document: withScout(`mutation { updateAgent(organizationId: "ACME", id: "SCOUT", input: {name: "Mine"}) { name } }`),
			wantCode: "FORBIDDEN"},
		{name: "delete", viewer: olivia, document: withScout(`mutation { deleteAgent(organizationId: "ACME", id: "SCOUT") }`), want: []string{`"deleteAgent":true`}},
		{name: "delete as a Member", viewer: maxim, document: withScout(`mutation { deleteAgent(organizationId: "ACME", id: "SCOUT") }`), wantCode: "FORBIDDEN"},
		{name: "delete an agent not there", viewer: olivia, document: withScout(`mutation { deleteAgent(organizationId: "ACME", id: "` + uuid.NewString() + `") }`),
			wantCode: "NOT_FOUND", wantReason: "agent.not_found"},
	})
}

func TestTaskMutations(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "assign", viewer: maxim, document: withScout(`mutation { assignTask(organizationId: "ACME", input: {agentId: "SCOUT", title: "Write the notes"}) { number state kind assignedBy { displayName } } }`),
			want: []string{`"assignTask":{"number":4,"state":"QUEUED","kind":"assigned","assignedBy":{"displayName":"Max"}}`}},
		{name: "assign on a branch", viewer: olivia,
			document: withScout(`mutation { assignTask(organizationId: "ACME", input: {agentId: "SCOUT", title: "Fix it", repositoryId: "WEBSITE", branch: "feature/login"}) { branch repositorySlug } }`),
			want:     []string{`"assignTask":{"branch":"feature/login","repositorySlug":"website"}`}},
		{name: "assign on a repository the assigner doesn't read", viewer: maxim,
			document: withScout(`mutation { assignTask(organizationId: "ACME", input: {agentId: "SCOUT", title: "Peek", repositoryId: "WEBSITE"}) { number } }`),
			wantCode: "BAD_USER_INPUT", wantReason: "repository.invalid"},
		{name: "assign to an agent of no ID", viewer: maxim, document: withScout(`mutation { assignTask(organizationId: "ACME", input: {agentId: "x", title: "Lost"}) { number } }`),
			wantCode: "BAD_USER_INPUT", wantReason: "agent.invalid"},
		{name: "cancel", viewer: maxim, document: withScout(`mutation { cancelTask(organizationId: "ACME", number: 1, expectedVersion: 1) { state cancelledBy { displayName } version } }`),
			want: []string{`"cancelTask":{"state":"CANCELLED","cancelledBy":{"displayName":"Max"},"version":2}`}},
		{name: "cancel one that ended", viewer: maxim, document: withScout(`mutation { cancelTask(organizationId: "ACME", number: 3) { state } }`),
			wantCode: "CONFLICT", wantReason: "task.ended"},
		{name: "cancel on a repository the viewer doesn't read", viewer: maxim, document: withScout(`mutation { cancelTask(organizationId: "ACME", number: 2) { state } }`),
			wantCode: "NOT_FOUND", wantReason: "task.not_found"},
	})
}
