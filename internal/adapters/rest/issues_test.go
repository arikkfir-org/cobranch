package rest

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/issues"
)

// withIssues is a viewer's fixture with Acme's issues, Max a Developer of website: ACME-1, by Max, assigned to him and
// about website, ACME-2 done, and ACME-3 in Todo, its description Markdown.
func withIssues(viewer func(f *orgFixture) *auth.Authenticated) func(f *orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		ctx := context.Background()
		f.store.Grant(f.max.User.ID, auth.RoleDeveloper, auth.ScopeRepository, f.website.ID)
		for _, in := range []struct {
			by *auth.Authenticated
			in issues.Input
		}{
			{&f.max, issues.Input{Title: "Show pushes", AssigneeID: f.max.User.ID, RepositoryID: f.website.ID, Priority: issues.PriorityHigh}},
			{&f.olivia, issues.Input{Title: "Done already", State: issues.StateDone}},
			{&f.olivia, issues.Input{Title: "Write docs", State: issues.StateTodo, Description: "See [the next](ACME-4)."}},
		} {
			if _, err := f.issues.Create(ctx, in.by.User, f.acme.ID, in.in); err != nil {
				panic(err)
			}
		}
		return viewer(f)
	}
}

var mergePatch = http.Header{"Content-Type": {"application/merge-patch+json"}}

func mergePatchAt(tag string) http.Header {
	return http.Header{"Content-Type": {"application/merge-patch+json"}, "If-Match": {tag}}
}

func TestCreateIssueAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a member creates one", viewer: maxim, method: http.MethodPost, path: fixed("/v1/orgs/acme/issues"),
			body: `{"title":"Show pushes","description":"**Now**","priority":"urgent","state":"todo"}`, wantStatus: 201,
			wantHeader: http.Header{"Location": {"/v1/orgs/acme/issues/1"}, "Etag": {`"1-ACME"`}}, check: func(t *testing.T, f *orgFixture, body []byte) {
				i := decode[Issue](t, body)
				if i.Identifier != "ACME-1" || i.Number != 1 || i.State != "todo" || i.Priority != "urgent" || i.CreatedBy == nil ||
					i.CreatedBy.Id != f.max.User.ID || i.DescriptionHtml == nil || !strings.Contains(*i.DescriptionHtml, "<strong>Now</strong>") {
					t.Errorf("issue = %s", body)
				}
			}},
		{name: "a priority that isn't", viewer: maxim, method: http.MethodPost, path: fixed("/v1/orgs/acme/issues"),
			body: `{"title":"x","priority":"asap"}`, wantStatus: 400},
		{name: "no title", viewer: maxim, method: http.MethodPost, path: fixed("/v1/orgs/acme/issues"), body: `{"title":""}`,
			wantStatus: 400, wantCode: "title.invalid"},
		{name: "an outsider learns nothing", viewer: nia, method: http.MethodPost, path: fixed("/v1/orgs/acme/issues"), body: `{"title":"x"}`,
			wantStatus: 404, wantCode: "organization.not_found"},
	})
}

func TestListIssuesAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "the open ones, newest first", viewer: withIssues(maxim), method: http.MethodGet, path: fixed("/v1/orgs/acme/issues"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				list := decode[struct{ Issues []Issue }](t, body).Issues
				if len(list) != 2 || list[0].Identifier != "ACME-3" || list[1].Identifier != "ACME-1" || list[1].Repository == nil ||
					list[1].Repository.Slug != "website" || list[0].DescriptionHtml != nil {
					t.Errorf("issues = %s", body)
				}
			}},
		{name: "a page, and a link to the next", viewer: withIssues(maxim), method: http.MethodGet,
			path: fixed("/v1/orgs/acme/issues?state=backlog&state=todo&state=done&limit=2"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if list := decode[struct{ Issues []Issue }](t, body).Issues; len(list) != 2 || list[1].Identifier != "ACME-2" {
					t.Errorf("issues = %s", body)
				}
			}},
		{name: "Max's", viewer: withIssues(maxim), method: http.MethodGet, path: func(f *orgFixture) string {
			return "/v1/orgs/acme/issues?assignee=" + f.max.User.ID.String()
		}, wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
			if list := decode[struct{ Issues []Issue }](t, body).Issues; len(list) != 1 || list[0].Identifier != "ACME-1" {
				t.Errorf("issues = %s", body)
			}
		}},
		{name: "nobody's", viewer: withIssues(maxim), method: http.MethodGet, path: fixed("/v1/orgs/acme/issues?assignee=none&q=docs"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if list := decode[struct{ Issues []Issue }](t, body).Issues; len(list) != 1 || list[0].Identifier != "ACME-3" {
					t.Errorf("issues = %s", body)
				}
			}},
		{name: "an assignee that isn't an ID", viewer: maxim, method: http.MethodGet, path: fixed("/v1/orgs/acme/issues?assignee=max"),
			wantStatus: 400, wantCode: "assignee.invalid"},
		{name: "a state that isn't", viewer: maxim, method: http.MethodGet, path: fixed("/v1/orgs/acme/issues?state=doing"), wantStatus: 400},
		{name: "a cursor that isn't", viewer: maxim, method: http.MethodGet, path: fixed("/v1/orgs/acme/issues?cursor=x"),
			wantStatus: 400, wantCode: "cursor.invalid"},
	})
}

func TestGetIssueAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "one, rendered, its links beside it", viewer: withIssues(maxim), method: http.MethodGet, path: fixed("/v1/orgs/acme/issues/3"),
			wantStatus: 200, wantHeader: http.Header{"Etag": {`"1-ACME"`}, "Cache-Control": {"private, no-cache"}}, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if i := decode[Issue](t, body); i.DescriptionHtml == nil || !strings.Contains(*i.DescriptionHtml, `href="/acme/issues/ACME-4"`) {
					t.Errorf("issue = %s", body)
				}
			}},
		{name: "one the caller holds", viewer: withIssues(maxim), method: http.MethodGet, path: fixed("/v1/orgs/acme/issues/3"),
			header: http.Header{"If-None-Match": {`"1-ACME"`}}, wantStatus: 304},
		{name: "one renamed since the caller read it", viewer: withIssues(maxim), method: http.MethodGet, path: fixed("/v1/orgs/acme/issues/3"),
			header: http.Header{"If-None-Match": {`"1-ACM"`}}, wantStatus: 200},
		{name: "one that isn't there", viewer: maxim, method: http.MethodGet, path: fixed("/v1/orgs/acme/issues/9"),
			wantStatus: 404, wantCode: "issue.not_found"},
	})
}

func TestUpdateIssueAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a merge patch", viewer: withIssues(maxim), method: http.MethodPatch, path: fixed("/v1/orgs/acme/issues/1"),
			body: `{"state":"in_progress","assigneeId":null,"repositoryId":null}`, header: mergePatchAt(`"1-ACME"`), wantStatus: 200,
			wantHeader: http.Header{"Etag": {`"2-ACME"`}}, check: func(t *testing.T, _ *orgFixture, body []byte) {
				i := decode[Issue](t, body)
				if i.State != "in_progress" || i.Assignee != nil || i.Repository != nil || i.Title != "Show pushes" || i.Priority != "high" || i.Version != 2 {
					t.Errorf("issue = %s", body)
				}
			}},
		{name: "a merge patch that assigns", viewer: withIssues(maxim), method: http.MethodPatch, path: fixed("/v1/orgs/acme/issues/2"),
			body: `{"state":"todo"}`, header: mergePatchAt("*"), wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if i := decode[Issue](t, body); i.ClosedAt != nil {
					t.Errorf("issue = %s, want it open", body)
				}
			}},
		{name: "a change from an old version", viewer: withIssues(maxim), method: http.MethodPatch, path: fixed("/v1/orgs/acme/issues/1"),
			body: `{"title":"x"}`, header: mergePatchAt(`"2-ACME"`), wantStatus: 412, wantCode: "precondition.failed"},
		{name: "a change made before the key changed", viewer: withIssues(maxim), method: http.MethodPatch, path: fixed("/v1/orgs/acme/issues/1"),
			body: `{"title":"x"}`, header: mergePatchAt(`"1-AC"`), wantStatus: 200},
		{name: "a change without If-Match", viewer: withIssues(maxim), method: http.MethodPatch, path: fixed("/v1/orgs/acme/issues/1"),
			body: `{"title":"x"}`, header: mergePatch, wantStatus: 428, wantCode: "precondition.required"},
	})
}

func TestDeleteIssueAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a member may not delete", viewer: withIssues(maxim), method: http.MethodDelete, path: fixed("/v1/orgs/acme/issues/1"),
			header: http.Header{"If-Match": {`"1"`}}, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "an owner deletes", viewer: withIssues(olivia), method: http.MethodDelete, path: fixed("/v1/orgs/acme/issues/1"),
			header: http.Header{"If-Match": {`"1"`}}, wantStatus: 204, check: func(t *testing.T, f *orgFixture, _ []byte) {
				if len(f.issueStore.Issues) != 2 {
					t.Errorf("%d issues left, want 2", len(f.issueStore.Issues))
				}
			}},
		{name: "a deletion without If-Match", viewer: withIssues(olivia), method: http.MethodDelete, path: fixed("/v1/orgs/acme/issues/1"),
			wantStatus: 428},
	})
}

func TestIssueActivityAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "its activity", viewer: withIssues(maxim), method: http.MethodGet, path: fixed("/v1/orgs/acme/issues/1/activity"), wantStatus: 200,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				events := decode[struct{ Events []IssueEvent }](t, body).Events
				if len(events) != 1 || events[0].Kind != "issue.created" || events[0].Actor == nil || events[0].Actor.Id != f.max.User.ID {
					t.Errorf("events = %s", body)
				}
			}},
	})
}

func TestIssueKeyAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "an organization's issue key", viewer: olivia, method: http.MethodGet, path: fixed("/v1/orgs/acme"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if o := decode[Organization](t, body); o.IssueKey != "ACME" {
					t.Errorf("organization = %s", body)
				}
			}},
		{name: "an owner changes it", viewer: olivia, method: http.MethodPatch, path: fixed("/v1/orgs/acme"), body: `{"issueKey":"AC"}`, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if o := decode[Organization](t, body); o.IssueKey != "AC" {
					t.Errorf("organization = %s", body)
				}
			}},
		{name: "a key that isn't one", viewer: olivia, method: http.MethodPatch, path: fixed("/v1/orgs/acme"), body: `{"issueKey":"ac"}`,
			wantStatus: 400, wantCode: "issue_key.invalid"},
	})
}

// withLinks is withIssues with website's live branches main, feature and fix, and feature linked to ACME-1 by Olivia.
func withLinks(viewer func(f *orgFixture) *auth.Authenticated) func(f *orgFixture) *auth.Authenticated {
	return withIssues(func(f *orgFixture) *auth.Authenticated {
		for _, name := range []string{"main", "feature", "fix"} {
			f.issueStore.AddBranch(f.website.ID, name)
		}
		if _, err := f.issues.Link(context.Background(), f.olivia.User, f.acme.ID, f.website.ID, "feature", 1); err != nil {
			panic(err)
		}
		return viewer(f)
	})
}

func TestBranchLinksAPI(t *testing.T) {
	const fix = "/v1/orgs/acme/repos/website/branches/fix/issue"
	runOrgCases(t, []orgCase{
		{name: "a member links a branch", viewer: withLinks(maxim), method: http.MethodPut, path: fixed(fix), body: `{"number":3}`, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[BranchIssue](t, body); got != (BranchIssue{Number: 3, Identifier: "ACME-3", Title: "Write docs", State: "in_progress",
					Priority: "none", LinkedBy: "hand"}) {
					t.Errorf("link = %s", body)
				}
			}},
		{name: "main works on no issue", viewer: withLinks(maxim), method: http.MethodPut, path: fixed("/v1/orgs/acme/repos/website/branches/main/issue"),
			body: `{"number":3}`, wantStatus: 400, wantCode: "branch.default"},
		{name: "an issue that isn't", viewer: withLinks(maxim), method: http.MethodPut, path: fixed(fix), body: `{"number":9}`, wantStatus: 400,
			wantCode: "issue.invalid"},
		{name: "a branch that doesn't live", viewer: withLinks(maxim), method: http.MethodPut, path: fixed("/v1/orgs/acme/repos/website/branches/gone/issue"),
			body: `{"number":3}`, wantStatus: 404, wantCode: "branch.not_found"},
		{name: "a repository the viewer doesn't read", viewer: maxim, method: http.MethodPut, path: fixed(fix), body: `{"number":3}`, wantStatus: 404,
			wantCode: "repository.not_found"},
		{name: "a member unlinks a branch", viewer: withLinks(maxim), method: http.MethodDelete, path: fixed("/v1/orgs/acme/repos/website/branches/feature/issue"),
			wantStatus: 204, check: func(t *testing.T, f *orgFixture, _ []byte) {
				if link, err := f.issues.OfBranch(context.Background(), f.olivia.User, f.acme.ID, f.website.ID, "feature"); err != nil || link != nil {
					t.Errorf("feature's issue after the unlink = %+v, %v", link, err)
				}
			}},
		{name: "an issue's branches", viewer: withLinks(maxim), method: http.MethodGet, path: fixed("/v1/orgs/acme/issues/1/branches"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[struct{ Branches []IssueBranch }](t, body).Branches
				if len(got) != 1 || got[0].Name != "feature" || got[0].Repository.Slug != "website" || got[0].LinkedBy != "hand" || got[0].Deleted {
					t.Errorf("branches = %s", body)
				}
			}},
		{name: "a branch shows its issue", viewer: withLinks(olivia), method: http.MethodGet, path: fixed("/v1/orgs/acme/repos/website/branches/feature"),
			wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if b := decode[Branch](t, body); b.Issue == nil || b.Issue.Identifier != "ACME-1" || b.Issue.LinkedBy != "hand" {
					t.Errorf("feature = %s, want ACME-1", body)
				}
			}},
		{name: "a token without issues.read sees no issue", viewer: withLinks(oliviaToken("repo.read")), method: http.MethodGet,
			path: fixed("/v1/orgs/acme/repos/website/branches/feature"), wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if strings.Contains(string(body), `"issue"`) {
					t.Errorf("feature = %s, want no issue", body)
				}
			}},
		{name: "the link in the activity", viewer: withLinks(maxim), method: http.MethodGet, path: fixed("/v1/orgs/acme/issues/1/activity"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				events := decode[struct{ Events []IssueEvent }](t, body).Events
				if len(events) != 3 || events[1].Kind != "issue.branch_linked" || events[1].Branch == nil || events[1].Branch.Name == nil ||
					*events[1].Branch.Name != "feature" || *events[1].Branch.Repository != "website" || events[2].Changes[0].Field != "state" {
					t.Errorf("events = %s", body)
				}
			}},
	})
}
