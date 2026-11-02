package rest

import (
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/agents/agentstest"
	"cobranch.dev/internal/services/auth"
)

// addAgents gives the fixture the agents' service, which knows the providers anthropic and openai, with Olivia's agent
// Scout and the built-in Reviewer, Scout's tasks: 1 by Max anywhere, 2 by Olivia on website's feature/login, which Max
// doesn't read, and 3 by Max, succeeded; and the secrets providerKey of Acme's and deployKey of website's.
func (f *orgFixture) addAgents(t *testing.T) {
	t.Helper()
	f.agentStore = agentstest.NewStore()
	f.agentStore.Now = now
	for _, v := range []auth.Authenticated{f.olivia, f.max, f.nia} {
		f.agentStore.Names[v.User.ID] = v.User.DisplayName
	}
	f.agentStore.Repos[f.website.ID] = agentstest.Repository{Org: f.acme.ID, Slug: "website"}
	f.agentStore.OrgSlugs[f.acme.ID] = "acme"
	f.agentStore.Branches[uuid.New()] = agentstest.Branch{Repo: f.website.ID, Name: "feature/login"}
	f.agents = agents.NewService(f.agentStore, auth.NewAuthorizer(f.store)).WithProviders("anthropic", "openai")
	f.providerKey, f.deployKey = f.agentStore.AddSecret(f.acme.ID, uuid.Nil, "anthropic-key"), f.agentStore.AddSecret(f.acme.ID, f.website.ID, "deploy-key")
	f.scout = f.agentStore.AddAgent(agents.Agent{OrgID: f.acme.ID, Name: "Scout", Instructions: "Be brief.", CreatedBy: &agents.Principal{ID: f.olivia.User.ID, DisplayName: "Olivia"}})
	f.agentStore.AddAgent(agents.Agent{OrgID: f.acme.ID, Name: "Reviewer", BuiltIn: true})
	for _, a := range []struct {
		by *auth.Authenticated
		in agents.TaskInput
	}{
		{&f.max, agents.TaskInput{Title: "Tidy up"}},
		{&f.olivia, agents.TaskInput{Title: "Fix the login", RepoID: f.website.ID, Branch: "feature/login"}},
		{&f.max, agents.TaskInput{Title: "Done already"}},
	} {
		if _, err := f.agents.Assign(t.Context(), a.by.User, f.acme.ID, f.scout.ID, a.in); err != nil {
			t.Fatal(err)
		}
	}
	f.agentStore.SetState(f.acme.ID, 3, agents.TaskSucceeded)
}

func scoutPath(f *orgFixture) string { return "/v1/orgs/acme/agents/" + f.scout.ID.String() }

func matchHeader(tag string) http.Header { return http.Header{"If-Match": {tag}} }

// held is a header that names, in If-None-Match, the ETag that viewer reads at path.
func held(viewer func(*orgFixture) *auth.Authenticated, path func(*orgFixture) string) func(*orgFixture) http.Header {
	return func(f *orgFixture) http.Header {
		return http.Header{"If-None-Match": {f.call(viewer(f), "GET", path(f), "").Header().Get("Etag")}}
	}
}

func TestAgents(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "list", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/agents"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[struct{ Agents []Agent }](t, body)
				if len(got.Agents) != 2 || got.Agents[0].Name != "Reviewer" || !got.Agents[0].BuiltIn || got.Agents[1].Name != "Scout" ||
					got.Agents[1].CreatedBy == nil || got.Agents[1].CreatedBy.DisplayName != "Olivia" || got.Agents[1].Grants == nil {
					t.Errorf("agents = %s", body)
				}
			}},
		{name: "list as an outsider", viewer: nia, method: "GET", path: fixed("/v1/orgs/acme/agents"), wantStatus: 404, wantCode: "organization.not_found"},
		{name: "create", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/agents"),
			body: `{"name":"Helper","description":"Helps","grants":[{"roleId":"` + auth.RoleMember.String() + `"}]}`, wantStatus: 201,
			wantTag: `"1-`,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				a := decode[Agent](t, body)
				if a.Name != "Helper" || a.Description != "Helps" || a.Version != 1 || len(a.Grants) != 1 || a.Grants[0].RoleName != "Member" || a.Grants[0].RepositoryId != nil {
					t.Errorf("agent = %s", body)
				}
			}},
		{name: "create with a name taken", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/agents"), body: `{"name":"scout"}`, wantStatus: 409, wantCode: "agent.name_taken"},
		{name: "create without a name", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/agents"), body: `{"name":" "}`, wantStatus: 400, wantCode: "agent.name_invalid"},
		{name: "create with a role beyond the creator's", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/agents"),
			body: `{"name":"Root","grants":[{"roleId":"` + auth.RoleOwner.String() + `"}]}`, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "create on a repository the creator doesn't read", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/agents"), wantStatus: 400, wantCode: "repository.invalid",
			bodyOf: func(f *orgFixture) string {
				return `{"name":"Peek","grants":[{"roleId":"` + auth.RoleReader.String() + `","repositoryId":"` + f.website.ID.String() + `"}]}`
			}},
		{name: "create as an outsider", viewer: nia, method: "POST", path: fixed("/v1/orgs/acme/agents"), body: `{"name":"Mine"}`, wantStatus: 404, wantCode: "organization.not_found"},
		{name: "get", viewer: maxim, method: "GET", path: scoutPath, wantStatus: 200, wantTag: `"1-`,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				if a := decode[Agent](t, body); a.Id != f.scout.ID || a.Name != "Scout" {
					t.Errorf("agent = %s", body)
				}
			}},
		{name: "get what the caller holds", viewer: maxim, method: "GET", path: scoutPath, headerFor: held(maxim, scoutPath), wantStatus: 304},
		{name: "get an agent not there", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/agents/" + uuid.NewString()), wantStatus: 404, wantCode: "agent.not_found"},
		{name: "update", viewer: olivia, method: "PATCH", path: scoutPath, header: matchHeader(`"1"`), body: `{"name":"Seeker","instructions":"Look first."}`, wantStatus: 200,
			wantTag: `"2-`,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if a := decode[Agent](t, body); a.Name != "Seeker" || a.Instructions != "Look first." || a.Version != 2 {
					t.Errorf("agent = %s", body)
				}
			}},
		{name: "update without If-Match", viewer: olivia, method: "PATCH", path: scoutPath, body: `{"name":"Seeker"}`, wantStatus: 428, wantCode: "precondition.required"},
		{name: "update from a stale version", viewer: olivia, method: "PATCH", path: scoutPath, header: matchHeader(`"2"`), body: `{"name":"Seeker"}`, wantStatus: 412, wantCode: "precondition.failed"},
		{name: "update another's agent", viewer: maxim, method: "PATCH", path: scoutPath, header: matchHeader("*"), body: `{"name":"Mine"}`, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "delete", viewer: olivia, method: "DELETE", path: scoutPath, header: matchHeader("*"), wantStatus: 204,
			check: func(t *testing.T, f *orgFixture, _ []byte) {
				if _, err := f.agentStore.Agent(t.Context(), f.acme.ID, f.scout.ID); err == nil {
					t.Error("Scout is still there")
				}
			}},
		{name: "delete without If-Match", viewer: olivia, method: "DELETE", path: scoutPath, wantStatus: 428, wantCode: "precondition.required"},
		{name: "delete as a Member", viewer: maxim, method: "DELETE", path: scoutPath, header: matchHeader(`"1"`), wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "delete a built-in agent", viewer: olivia, method: "DELETE", header: matchHeader("*"), wantStatus: 409, wantCode: "agent.built_in",
			path: func(f *orgFixture) string {
				list, _ := f.agentStore.Agents(t.Context(), f.acme.ID)
				return "/v1/orgs/acme/agents/" + list[0].ID.String()
			}},
	})
}

func taskNumbers(t *testing.T, body []byte) []int64 {
	t.Helper()
	var out []int64
	for _, task := range decode[struct{ Tasks []Task }](t, body).Tasks {
		out = append(out, task.Number)
	}
	return out
}

func wantTasks(want ...int64) func(*testing.T, *orgFixture, []byte) {
	return func(t *testing.T, _ *orgFixture, body []byte) {
		if got := taskNumbers(t, body); !slices.Equal(got, want) {
			t.Errorf("tasks = %v, want %v", got, want)
		}
	}
}

func TestTasks(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "list", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/tasks"), wantStatus: 200, check: wantTasks(3, 2, 1)},
		{name: "list leaves out repositories the viewer doesn't read", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/tasks"), wantStatus: 200, check: wantTasks(3, 1)},
		{name: "list a page", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/tasks?state=queued&state=running&limit=1"), wantStatus: 200,
			wantHeader: http.Header{"Link": {`<?cursor=2&limit=1&state=queued&state=running>; rel="next"`}}, check: wantTasks(2)},
		{name: "list the next page", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/tasks?cursor=2&limit=1&state=queued"), wantStatus: 200, check: wantTasks(1)},
		{name: "list a repository's and an agent's", viewer: olivia, method: "GET", wantStatus: 200, check: wantTasks(2),
			path: func(f *orgFixture) string {
				return "/v1/orgs/acme/tasks?repositoryId=" + f.website.ID.String() + "&agent=" + f.scout.ID.String()
			}},
		{name: "list a state not there", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/tasks?state=lost"), wantStatus: 400},
		{name: "list from a bad cursor", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/tasks?cursor=x"), wantStatus: 400, wantCode: "cursor.invalid"},
		{name: "list as an outsider", viewer: nia, method: "GET", path: fixed("/v1/orgs/acme/tasks"), wantStatus: 404, wantCode: "organization.not_found"},
	})
}

func TestTaskChanges(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "assign", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/tasks"), wantStatus: 201,
			wantTag: `"1-`, wantHeader: http.Header{"Location": {"/v1/orgs/acme/tasks/4"}},
			bodyOf: func(f *orgFixture) string {
				return `{"agentId":"` + f.scout.ID.String() + `","title":"Write the notes","instructions":"Short ones."}`
			},
			check: func(t *testing.T, f *orgFixture, body []byte) {
				task := decode[Task](t, body)
				if task.Number != 4 || task.State != "queued" || task.Kind != "assigned" || task.AssignedBy == nil || task.AssignedBy.Id != f.max.User.ID ||
					task.Agent.Id != f.scout.ID || task.RepositoryId != nil || task.StartedAt != nil {
					t.Errorf("task = %s", body)
				}
			}},
		{name: "assign on a branch", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/tasks"), wantStatus: 201,
			bodyOf: func(f *orgFixture) string {
				return `{"agentId":"` + f.scout.ID.String() + `","title":"Fix it","repositoryId":"` + f.website.ID.String() + `","branch":"refs/heads/feature/login"}`
			},
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if task := decode[Task](t, body); task.Branch == nil || *task.Branch != "feature/login" || task.RepositorySlug == nil || *task.RepositorySlug != "website" {
					t.Errorf("task = %s", body)
				}
			}},
		{name: "assign on a repository the assigner doesn't read", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/tasks"), wantStatus: 400, wantCode: "repository.invalid",
			bodyOf: func(f *orgFixture) string {
				return `{"agentId":"` + f.scout.ID.String() + `","title":"Peek","repositoryId":"` + f.website.ID.String() + `"}`
			}},
		{name: "assign to an agent not there", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/tasks"), wantStatus: 400, wantCode: "agent.invalid",
			body: `{"agentId":"` + uuid.NewString() + `","title":"Lost"}`},
		{name: "assign as an outsider", viewer: nia, method: "POST", path: fixed("/v1/orgs/acme/tasks"), wantStatus: 404, wantCode: "organization.not_found",
			bodyOf: func(f *orgFixture) string { return `{"agentId":"` + f.scout.ID.String() + `","title":"Mine"}` }},
		{name: "get", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/tasks/2"), wantStatus: 200, wantTag: `"1-`,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				if task := decode[Task](t, body); task.Number != 2 || task.Title != "Fix the login" || task.RepositoryId == nil || *task.RepositoryId != f.website.ID {
					t.Errorf("task = %s", body)
				}
			}},
		{name: "get what the caller holds", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/tasks/2"), headerFor: held(olivia, fixed("/v1/orgs/acme/tasks/2")), wantStatus: 304},
		{name: "get on a repository the viewer doesn't read", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/tasks/2"), wantStatus: 404, wantCode: "task.not_found"},
		{name: "cancel", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/tasks/1/cancel"), wantStatus: 200, wantTag: `"2-`,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				task := decode[Task](t, body)
				if task.State != "cancelled" || task.CancelledBy == nil || task.CancelledBy.Id != f.max.User.ID || task.EndedAt == nil {
					t.Errorf("task = %s", body)
				}
			}},
		{name: "cancel from a stale version", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/tasks/1/cancel"), header: matchHeader(`"2"`), wantStatus: 412, wantCode: "precondition.failed"},
		{name: "cancel one that ended", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/tasks/3/cancel"), wantStatus: 409, wantCode: "task.ended"},
		{name: "cancel on a repository the viewer doesn't read", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/tasks/2/cancel"), wantStatus: 404, wantCode: "task.not_found"},
	})
}

// TestTaskCommit checks that a task answers the commit it works at, such as a review task's head, and leaves sha out
// for none.
func TestTaskCommit(t *testing.T) {
	head := "0123456789abcdef0123456789abcdef01234567"
	for _, tt := range []struct {
		name string
		sha  string
		want *string
	}{
		{name: "a review task at its head", sha: head, want: &head},
		{name: "a task at no commit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := task(agents.Task{Kind: agents.KindReview, SHA: tt.sha}).Sha
			if (got == nil) != (tt.want == nil) || got != nil && *got != *tt.want {
				t.Errorf("sha = %v, want %v", got, tt.want)
			}
		})
	}
}
