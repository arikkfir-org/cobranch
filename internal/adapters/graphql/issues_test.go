package graphql

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

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
			document: fixed(`{ organization(slug: "acme") { issueKey issues { issues { identifier state priority assignee { displayName } repository { slug } descriptionHtml branch { name } } nextCursor counts { open closed } } } }`),
			want: []string{`"issueKey":"ACME"`, `{"identifier":"ACME-3","state":"TODO","priority":"NONE","assignee":null,"repository":null,"descriptionHtml":null,"branch":null}`,
				`{"identifier":"ACME-1","state":"BACKLOG","priority":"HIGH","assignee":{"displayName":"Max"},"repository":{"slug":"website"},"descriptionHtml":null,"branch":null}`,
				`"nextCursor":null`, `"counts":{"open":2,"closed":1}`}},
		{name: "the closed ones, counted with the open", viewer: withIssues(maxim),
			document: fixed(`{ organization(slug: "acme") { issues(filter: {states: [DONE, CANCELED], text: "o"}) { issues { identifier } counts { open closed } } } }`),
			want:     []string{`"issues":[{"identifier":"ACME-2"}]`, `"counts":{"open":2,"closed":1}`}},
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
		{name: "a patch that hands it to an agent", viewer: withIssues(maxim), document: func(f *orgFixture) string {
			return onAcme(`mutation { updateIssue(organizationId: "$org", number: 1, input: {assigneeId: "` + f.scout.ID.String() + `"}) { assignee { displayName } task { number state } } }`)(f)
		}, want: []string{`"assignee":{"displayName":"Scout"},"task":{"number":4,"state":"QUEUED"}`}},
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

// onWebsite is document with $org and $repo naming Acme and website.
func onWebsite(document string) func(*orgFixture) string {
	return func(f *orgFixture) string {
		return strings.NewReplacer("$org", f.acme.ID.String(), "$repo", f.website.ID.String()).Replace(document)
	}
}

func TestBranchLinks(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a member links a branch", viewer: withLinks(maxim),
			document: onWebsite(`mutation { linkBranch(organizationId: "$org", repositoryId: "$repo", branch: "fix", number: 3) { issue { identifier state } linkedBy } }`),
			want:     []string{`"linkBranch":{"issue":{"identifier":"ACME-3","state":"IN_PROGRESS"},"linkedBy":"HAND"}`}},
		{name: "main works on no issue", viewer: withLinks(maxim),
			document: onWebsite(`mutation { linkBranch(organizationId: "$org", repositoryId: "$repo", branch: "main", number: 3) { linkedBy } }`),
			wantCode: badUserInput, wantReason: "branch.default"},
		{name: "a branch that doesn't live", viewer: withLinks(maxim),
			document: onWebsite(`mutation { linkBranch(organizationId: "$org", repositoryId: "$repo", branch: "gone", number: 3) { linkedBy } }`),
			wantCode: notFound, wantReason: "branch.not_found"},
		{name: "a member unlinks a branch", viewer: withLinks(maxim),
			document: onWebsite(`mutation { unlinkBranch(organizationId: "$org", repositoryId: "$repo", branch: "feature") }`),
			want:     []string{`"unlinkBranch":true`}},
		{name: "an issue's branches and the link in its activity", viewer: withLinks(maxim),
			document: fixed(`{ organization(slug: "acme") { issue(number: 1) { state branches { repository { slug } name deleted linkedBy } activity { kind branch { repository name linkedBy } } } } }`),
			want: []string{`"state":"IN_PROGRESS"`, `"branches":[{"repository":{"slug":"website"},"name":"feature","deleted":false,"linkedBy":"HAND"}]`,
				`{"kind":"issue.branch_linked","branch":{"repository":"website","name":"feature","linkedBy":"HAND"}}`}},
		{name: "a task's start in an issue's activity", viewer: withIssues(func(f *orgFixture) *auth.Authenticated {
			for id := range f.issueStore.Events {
				f.issueStore.Events[id] = append(f.issueStore.Events[id], issues.Event{ID: uuid.New(), Kind: "issue.task_started", At: time.Now(),
					Task: &issues.EventTask{Number: 318, RepoID: f.website.ID, Branch: "feature/push-worker"}})
			}
			return &f.max
		}),
			document: fixed(`{ organization(slug: "acme") { issue(number: 1) { activity { kind task { number branch } } } } }`),
			want:     []string{`{"kind":"issue.task_started","task":{"number":318,"branch":"feature/push-worker"}}`}},
		{name: "an issue shows the live branch linked to it last", viewer: withLinks(maxim),
			document: fixed(`{ organization(slug: "acme") { issues { issues { identifier branch { repositoryId repositorySlug name } } } } }`),
			check: func(t *testing.T, f *orgFixture, body string) {
				want := `{"identifier":"ACME-1","branch":{"repositoryId":"` + f.website.ID.String() + `","repositorySlug":"website","name":"feature"}}`
				if !strings.Contains(body, want) {
					t.Errorf("body = %s, want %s", body, want)
				}
			}},
		{name: "a branch shows its issue", viewer: withLinks(olivia),
			document: fixed(`{ organization(slug: "acme") { repository(slug: "website") { feature: branch(name: "feature") { issue { issue { identifier } linkedBy } } main: branch(name: "main") { issue { linkedBy } } } } }`),
			want:     []string{`"feature":{"issue":{"issue":{"identifier":"ACME-1"},"linkedBy":"HAND"}}`, `"main":{"issue":null}`}},
	})
}
