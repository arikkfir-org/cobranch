package graphql

import (
	"context"
	"strings"
	"testing"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/issues"
)

// withIssues is a viewer of a fixture with Acme's issues, Max a Developer of website: ACME-1, by Max, assigned to him
// and about website, ACME-2 done, and ACME-3 in Todo, its description Markdown.
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
			{&f.olivia, issues.Input{Title: "Write docs", State: issues.StateTodo, Description: "**Soon**"}},
		} {
			if _, err := f.issues.Create(ctx, in.by.User, f.acme.ID, in.in); err != nil {
				panic(err)
			}
		}
		return viewer(f)
	}
}

func onAcme(document string) func(*orgFixture) string {
	return func(f *orgFixture) string { return strings.ReplaceAll(document, "$org", f.acme.ID.String()) }
}

func TestIssues(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "the open ones, newest first", viewer: withIssues(maxim),
			document: fixed(`{ organization(slug: "acme") { issueKey issues { issues { identifier state priority assignee { displayName } repository { slug } descriptionHtml } nextCursor } } }`),
			want: []string{`"issueKey":"ACME"`, `{"identifier":"ACME-3","state":"TODO","priority":"NONE","assignee":null,"repository":null,"descriptionHtml":null}`,
				`{"identifier":"ACME-1","state":"BACKLOG","priority":"HIGH","assignee":{"displayName":"Max"},"repository":{"slug":"website"},"descriptionHtml":null}`,
				`"nextCursor":null`}},
		{name: "a page and its cursor", viewer: withIssues(maxim),
			document: fixed(`{ organization(slug: "acme") { issues(filter: {states: [BACKLOG, DONE]}, first: 1) { issues { identifier } nextCursor } } }`),
			want:     []string{`"issues":[{"identifier":"ACME-2"}]`, `"nextCursor":"`}},
		{name: "nobody's, by text", viewer: withIssues(maxim),
			document: fixed(`{ organization(slug: "acme") { issues(filter: {assigneeId: "none", text: "docs"}) { issues { identifier } } } }`),
			want:     []string{`"issues":[{"identifier":"ACME-3"}]`}},
		{name: "an assignee that isn't an ID", viewer: maxim,
			document: fixed(`{ organization(slug: "acme") { issues(filter: {assigneeId: "max"}) { issues { identifier } } } }`),
			wantCode: badUserInput, wantReason: "assignee.invalid"},
		{name: "one, rendered, with its activity", viewer: withIssues(maxim),
			document: fixed(`{ organization(slug: "acme") { issue(number: 3) { identifier descriptionHtml version activity { kind actor { displayName } changes { field } } } } }`),
			want:     []string{`"identifier":"ACME-3"`, `\u003cstrong\u003eSoon`, `"activity":[{"kind":"issue.created","actor":{"displayName":"Olivia"},"changes":[]}]`}},
		{name: "one that isn't there", viewer: maxim, document: fixed(`{ organization(slug: "acme") { issue(number: 9) { identifier } } }`),
			want: []string{`"issue":null`}},
		{name: "a member creates one", viewer: maxim,
			document: onAcme(`mutation { createIssue(organizationId: "$org", input: {title: "Show pushes", priority: URGENT, state: TODO}) { identifier state priority createdBy { displayName } version } }`),
			want:     []string{`"identifier":"ACME-1","state":"TODO","priority":"URGENT","createdBy":{"displayName":"Max"},"version":1`}},
		{name: "no title", viewer: maxim, document: onAcme(`mutation { createIssue(organizationId: "$org", input: {title: " "}) { identifier } }`),
			wantCode: badUserInput, wantReason: "title.invalid"},
		{name: "an outsider learns nothing", viewer: nia, document: onAcme(`mutation { createIssue(organizationId: "$org", input: {title: "x"}) { identifier } }`),
			wantCode: notFound, wantReason: "organization.not_found"},
		{name: "a patch that clears", viewer: withIssues(maxim),
			document: onAcme(`mutation { updateIssue(organizationId: "$org", number: 1, input: {state: IN_PROGRESS, assigneeId: null}, expectedVersion: 1) { state assignee { displayName } repository { slug } priority version } }`),
			want:     []string{`"state":"IN_PROGRESS","assignee":null,"repository":{"slug":"website"},"priority":"HIGH","version":2`}},
		{name: "a patch from an old version", viewer: withIssues(maxim),
			document: onAcme(`mutation { updateIssue(organizationId: "$org", number: 1, input: {title: "x"}, expectedVersion: 2) { version } }`),
			wantCode: conflict, wantReason: "precondition.failed"},
		{name: "a member may not delete", viewer: withIssues(maxim), document: onAcme(`mutation { deleteIssue(organizationId: "$org", number: 1) }`),
			wantCode: forbidden},
		{name: "an owner deletes", viewer: withIssues(olivia), document: onAcme(`mutation { deleteIssue(organizationId: "$org", number: 1, expectedVersion: 1) }`),
			want: []string{`"deleteIssue":true`}},
		{name: "an owner changes the issue key", viewer: olivia,
			document: func(f *orgFixture) string {
				return `mutation { updateOrganization(id: "` + f.acme.ID.String() + `", input: {slug: "acme", name: "Acme", membershipRoleId: "` +
					auth.RoleMember.String() + `", acceptsJoinRequests: true, issueKey: "AC"}) { issueKey } }`
			},
			want: []string{`"issueKey":"AC"`}},
	})
}
