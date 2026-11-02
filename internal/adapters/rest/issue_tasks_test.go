package rest

import (
	"context"
	"net/http"
	"testing"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/issues"
)

// withScoutsIssue is withIssues with ACME-1 assigned to Scout by Max, which gives Scout task 4.
func withScoutsIssue(viewer func(f *orgFixture) *auth.Authenticated) func(f *orgFixture) *auth.Authenticated {
	return withIssues(func(f *orgFixture) *auth.Authenticated {
		scout := f.scout.ID
		if _, err := f.issues.Update(context.Background(), f.max.User, f.acme.ID, 1, issues.Patch{AssigneeID: &scout}); err != nil {
			panic(err)
		}
		return viewer(f)
	})
}

func TestAssignIssueToAgentAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a member assigns an agent", viewer: withIssues(maxim), method: http.MethodPatch, path: fixed("/v1/orgs/acme/issues/1"),
			bodyOf: func(f *orgFixture) string { return `{"assigneeId":"` + f.scout.ID.String() + `"}` }, header: mergePatchAt(`"1-ACME"`),
			wantStatus: 200, wantHeader: http.Header{"Etag": {`"2-ACME-4-queued"`}}, check: func(t *testing.T, f *orgFixture, body []byte) {
				i := decode[Issue](t, body)
				if i.Assignee == nil || i.Assignee.Id != f.scout.ID || i.Task == nil || i.Task.Number != 4 || i.Task.State != "queued" {
					t.Errorf("issue = %s", body)
				}
				task, err := f.agentStore.Task(context.Background(), f.acme.ID, 4)
				if err != nil || task.Kind != issues.KindTask || task.IssueID != i.Id || task.Title != "ACME-1 Show pushes" || task.RepoID != f.website.ID {
					t.Errorf("task = %+v, %v", task, err)
				}
			}},
		{name: "its task shows on the issue", viewer: withScoutsIssue(maxim), method: http.MethodGet, path: fixed("/v1/orgs/acme/issues/1"),
			wantStatus: 200, wantHeader: http.Header{"Etag": {`"2-ACME-4-queued"`}}, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if i := decode[Issue](t, body); i.Task == nil || i.Task.Number != 4 {
					t.Errorf("issue = %s", body)
				}
			}},
		{name: "a task that moved on reads anew", viewer: func(f *orgFixture) *auth.Authenticated {
			v := withScoutsIssue(maxim)(f)
			f.agentStore.SetState(f.acme.ID, 4, agents.TaskRunning)
			return v
		}, method: http.MethodGet, path: fixed("/v1/orgs/acme/issues/1"), header: http.Header{"If-None-Match": {`"2-ACME-4-queued"`}},
			wantStatus: 200, wantHeader: http.Header{"Etag": {`"2-ACME-4-running"`}}},
		{name: "reassigning cancels the task", viewer: withScoutsIssue(maxim), method: http.MethodPatch, path: fixed("/v1/orgs/acme/issues/1"),
			bodyOf: func(f *orgFixture) string { return `{"assigneeId":"` + f.max.User.ID.String() + `"}` }, header: mergePatchAt("*"),
			wantStatus: 200, wantHeader: http.Header{"Etag": {`"3-ACME"`}}, check: func(t *testing.T, f *orgFixture, body []byte) {
				if i := decode[Issue](t, body); i.Task != nil {
					t.Errorf("issue = %s, want no task", body)
				}
				if task, err := f.agentStore.Task(context.Background(), f.acme.ID, 4); err != nil || task.State != agents.TaskCancelled {
					t.Errorf("task = %+v, %v; want cancelled", task, err)
				}
			}},
		{name: "a principal of no organization's", viewer: withIssues(maxim), method: http.MethodPatch, path: fixed("/v1/orgs/acme/issues/1"),
			bodyOf: func(f *orgFixture) string { return `{"assigneeId":"` + f.nia.User.ID.String() + `"}` }, header: mergePatchAt("*"),
			wantStatus: 400, wantCode: "assignee.invalid"},
	})
}
