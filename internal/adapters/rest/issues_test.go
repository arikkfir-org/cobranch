package rest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/markdown"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/issues"
	"cobranch.dev/internal/services/issues/issuestest"
	"cobranch.dev/internal/services/live/livetest"
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
				got := decode[struct {
					Issues []Issue
					Counts IssueCounts
				}](t, body)
				list := got.Issues
				if len(list) != 2 || list[0].Identifier != "ACME-3" || list[1].Identifier != "ACME-1" || list[1].Repository == nil ||
					list[1].Repository.Slug != "website" || list[0].DescriptionHtml != nil || list[0].Branch != nil {
					t.Errorf("issues = %s", body)
				}
				if got.Counts != (IssueCounts{Open: 2, Closed: 1}) {
					t.Errorf("counts = %+v, want 2 open and 1 closed", got.Counts)
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
		{name: "the closed ones, counted with the open", viewer: withIssues(maxim), method: http.MethodGet,
			path: fixed("/v1/orgs/acme/issues?state=done&state=canceled&q=o"), wantStatus: 200, check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[struct {
					Issues []Issue
					Counts IssueCounts
				}](t, body)
				if len(got.Issues) != 1 || got.Issues[0].Identifier != "ACME-2" || got.Counts != (IssueCounts{Open: 2, Closed: 1}) {
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
		{name: "a task's start", viewer: withTaskStart(withIssues(maxim)), method: http.MethodGet, path: fixed("/v1/orgs/acme/issues/1/activity"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				events := decode[struct{ Events []IssueEvent }](t, body).Events
				if len(events) != 2 || events[1].Kind != "issue.task_started" || events[1].Task == nil || events[1].Task.Number != 318 ||
					events[1].Task.Branch == nil || *events[1].Task.Branch != "feature/push-worker" {
					t.Errorf("events = %s", body)
				}
			}},
	})
}

// withTaskStart is viewer's fixture with an agent's start of task 318 on ACME-1, on website's feature/push-worker.
func withTaskStart(viewer func(f *orgFixture) *auth.Authenticated) func(f *orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		v := viewer(f)
		for id := range f.issueStore.Events {
			f.issueStore.Events[id] = append(f.issueStore.Events[id], issues.Event{ID: uuid.New(), Kind: "issue.task_started", At: time.Now(),
				Task: &issues.EventTask{Number: 318, RepoID: f.website.ID, Branch: "feature/push-worker"}})
		}
		return v
	}
}

func TestIssueKeyAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "an organization's issue key", viewer: olivia, method: http.MethodGet, path: fixed("/v1/orgs/acme"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if o := decode[Organization](t, body); o.IssueKey != "ACME" {
					t.Errorf("organization = %s", body)
				}
			}},
		{name: "an owner changes it", viewer: olivia, method: http.MethodPatch, path: fixed("/v1/orgs/acme"), body: `{"issueKey":"AC"}`, header: ifMatchHeader("*"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if o := decode[Organization](t, body); o.IssueKey != "AC" {
					t.Errorf("organization = %s", body)
				}
			}},
		{name: "a key that isn't one", viewer: olivia, method: http.MethodPatch, path: fixed("/v1/orgs/acme"), body: `{"issueKey":"ac"}`, header: ifMatchHeader("*"),
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
		{name: "an issue shows the live branch linked to it last", viewer: withLinks(maxim), method: http.MethodGet, path: fixed("/v1/orgs/acme/issues/1"),
			wantStatus: 200, wantTag: `"2-ACME-b`, check: func(t *testing.T, f *orgFixture, body []byte) {
				if b := decode[Issue](t, body).Branch; b == nil || b.Name != "feature" || b.Repository.Slug != "website" || b.Repository.Id != f.website.ID {
					t.Errorf("ACME-1 = %s, want website's feature", body)
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

// issueLists are what Acme's issue lists and ACME-1's page read, which a read may wait on.
var issueLists = []string{"/v1/orgs/acme/issues", "/v1/orgs/acme/issues?state=done&limit=1", "/v1/orgs/acme/issues/1",
	"/v1/orgs/acme/issues/1/activity", "/v1/orgs/acme/issues/1/branches"}

func TestIssueListsTagged(t *testing.T) {
	for _, list := range issueLists {
		t.Run(list, func(t *testing.T) {
			stale := http.Header{"If-None-Match": {`"stale"`}}
			wait := "&wait="
			if !strings.Contains(list, "?") {
				wait = "?wait="
			}
			runOrgCases(t, []orgCase{
				{name: "tagged", viewer: withIssues(olivia), method: "GET", path: fixed(list), wantStatus: 200, wantTag: `"`},
				{name: "still the list the caller holds", viewer: withIssues(olivia), method: "GET", path: fixed(list), wantStatus: 304, wantTag: `"`,
					headerFor: held(olivia, fixed(list))},
				{name: "a wait that sees no change", viewer: withIssues(olivia), method: "GET", path: fixed(list + wait + "1"), wantStatus: 304,
					wantTag: `"`, headerFor: held(olivia, fixed(list))},
				{name: "another list", viewer: withIssues(olivia), method: "GET", path: fixed(list + wait + "15"), header: stale, wantStatus: 200, wantTag: `"`},
				{name: "a wait without a tag answers at once", viewer: withIssues(olivia), method: "GET", path: fixed(list + wait + "15"), wantStatus: 200},
				{name: "a wait past 15 seconds", viewer: withIssues(olivia), method: "GET", path: fixed(list + wait + "16"), header: stale,
					wantStatus: 400, wantCode: "wait.invalid"},
				{name: "a wait as an outsider", viewer: withIssues(nia), method: "GET", path: fixed(list + wait + "1"), header: stale, wantStatus: 404,
					wantCode: "organization.not_found"},
			})
		})
	}
}

func TestIssueWaitGone(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "an issue there isn't", viewer: withIssues(olivia), method: "GET", path: fixed("/v1/orgs/acme/issues/9?wait=1"),
			header: http.Header{"If-None-Match": {`"1-ACME"`}}, wantStatus: 404, wantCode: "issue.not_found"},
	})
}

// TestBranchWaitsOnItsIssue checks that a branch's read waiting for a change answers once the branch is linked to
// another issue or unlinked, which changes no branch: a branch's page shows the issue its branch works on.
func TestBranchWaitsOnItsIssue(t *testing.T) {
	t.Parallel()
	const path = "/v1/orgs/acme/repos/website/branches/feature"
	tests := []struct {
		name   string
		change func(ctx context.Context, t *testing.T, f *orgFixture)
		want   string
	}{
		{name: "the branch unlinked", want: "", change: func(ctx context.Context, t *testing.T, f *orgFixture) {
			if err := f.issues.Unlink(ctx, f.olivia.User, f.acme.ID, f.website.ID, "feature"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "the branch linked to another issue", want: "ACME-3", change: func(ctx context.Context, t *testing.T, f *orgFixture) {
			if _, err := f.issues.Link(ctx, f.olivia.User, f.acme.ID, f.website.ID, "feature", 3); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newOrgFixture(t)
			viewer := withLinks(olivia)(f)
			f.issues = issues.NewService(&lockedIssues{Store: f.issueStore}, markdown.New(nil, nil), auth.NewAuthorizer(f.store)).WithAgents(f.agents, f.agentStore)
			f.issues.SetClock(func() time.Time { return now })
			notifier := &livetest.Notifier{}
			f.code.WithNotifier(notifier)
			f.issues.WithNotifier(notifier)
			rec := waitThrough(t, f, viewer, path, notifier, f.website.ID, func(ctx context.Context) { tt.change(ctx, t, f) })
			got := ""
			if issue := decode[Branch](t, rec.Body.Bytes()).Issue; issue != nil {
				got = issue.Identifier
			}
			if rec.Code != http.StatusOK || got != tt.want {
				t.Errorf("after the change = %d, issue %q; want 200, issue %q: %s", rec.Code, got, tt.want, rec.Body)
			}
		})
	}
}

// lockedIssues is the issues' store a test links a branch in while a request follows the branch: the reads the
// branch's follower makes of its link, and the link, take its lock.
type lockedIssues struct {
	*issuestest.Store
	mu sync.Mutex
}

func (s *lockedIssues) Branch(ctx context.Context, orgID, repoID uuid.UUID, name string) (issues.Branch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.Store.Branch(ctx, orgID, repoID, name)
	if err != nil {
		return issues.Branch{}, fmt.Errorf("reading the branch: %w", err)
	}
	return b, nil
}

func (s *lockedIssues) ByID(ctx context.Context, issueID uuid.UUID) (issues.Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	issue, err := s.Store.ByID(ctx, issueID)
	if err != nil {
		return issues.Issue{}, fmt.Errorf("reading the issue: %w", err)
	}
	return issue, nil
}

func (s *lockedIssues) Link(ctx context.Context, actor uuid.UUID, branch issues.Branch, issueID *uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Store.Link(ctx, actor, branch, issueID); err != nil {
		return fmt.Errorf("linking the branch: %w", err)
	}
	return nil
}

// waitThrough has viewer read path with the ETag of what it reads now and a wait, makes change once both the read's
// followers watch id, wakes id, and answers the read's answer, or fails after a few seconds.
func waitThrough(t *testing.T, f *orgFixture, viewer *auth.Authenticated, path string, notifier *livetest.Notifier, id uuid.UUID,
	change func(ctx context.Context)) *httptest.ResponseRecorder {
	t.Helper()
	tag := http.Header{"If-None-Match": {f.callWith(viewer, "GET", path, "", nil).Header().Get("Etag")}}
	answered := make(chan *httptest.ResponseRecorder, 1)
	go func() { answered <- f.callWith(viewer, "GET", path+"?wait=15", "", tag) }()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for notifier.Watching(id) < 2 && ctx.Err() == nil {
		time.Sleep(10 * time.Millisecond)
	}
	change(ctx)
	notifier.Wake(id)
	select {
	case rec := <-answered:
		return rec
	case <-time.After(10 * time.Second):
		t.Fatal("no answer after the change")
		return nil
	}
}
