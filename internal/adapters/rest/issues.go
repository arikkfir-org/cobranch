package rest

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"

	"cobranch.dev/internal/services/issues"
)

// Issues is what the API needs of the issues' service, its organizations' labels included.
type Issues interface {
	Labels
	List(ctx context.Context, v issues.Viewer, orgID uuid.UUID, f issues.Filter) (issues.Page, error)
	FollowList(ctx context.Context, v issues.Viewer, orgID uuid.UUID, f issues.Filter) (<-chan issues.Page, error)
	Create(ctx context.Context, v issues.Viewer, orgID uuid.UUID, in issues.Input) (issues.Issue, error)
	Get(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int) (issues.Issue, error)
	FollowIssue(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int) (<-chan issues.Issue, error)
	Update(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int, p issues.Patch) (issues.Issue, error)
	Delete(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int, version int64) error
	Activity(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int) ([]issues.Event, error)
	FollowActivity(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int) (<-chan []issues.Event, error)
	Branches(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int) ([]issues.LinkedBranch, error)
	FollowBranches(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int) (<-chan []issues.LinkedBranch, error)
	OfBranch(ctx context.Context, v issues.Viewer, orgID, repoID uuid.UUID, name string) (*issues.Link, error)
	FollowBranchLink(ctx context.Context, v issues.Viewer, orgID, repoID uuid.UUID, name string) (<-chan *issues.Link, error)
	Link(ctx context.Context, v issues.Viewer, orgID, repoID uuid.UUID, name string, number int) (issues.Link, error)
	Unlink(ctx context.Context, v issues.Viewer, orgID, repoID uuid.UUID, name string) error
}

// issueOf is i as the API shows it.
func issueOf(i issues.Issue) Issue {
	out := Issue{Id: i.ID, Number: i.Number, Identifier: i.Identifier(), Title: i.Title, Description: i.Description,
		State: IssueState(i.State), Priority: IssuePriority(i.Priority.String()), CreatedAt: i.CreatedAt.UTC(), UpdatedAt: i.UpdatedAt.UTC(),
		Version: i.Version}
	if i.DescriptionHTML != "" {
		out.DescriptionHtml = &i.DescriptionHTML
	}
	if i.Assignee != nil {
		out.Assignee = &Principal{Id: i.Assignee.ID, DisplayName: i.Assignee.DisplayName}
	}
	if t := i.Task; t != nil {
		out.Task = &IssueTask{Number: t.Number, State: TaskState(t.State)}
	}
	if i.CreatedBy != nil {
		out.CreatedBy = &Principal{Id: i.CreatedBy.ID, DisplayName: i.CreatedBy.DisplayName}
	}
	if r := i.Repository; r != nil {
		out.Repository = &IssueRepository{Id: r.ID, Slug: r.Slug, Name: r.Name}
	}
	if b := i.Branch; b != nil {
		out.Branch = &IssueLiveBranch{Name: b.Name}
		out.Branch.Repository.Id, out.Branch.Repository.Slug = b.Repository.ID, b.Repository.Slug
	}
	out.Labels = mapAll(i.Labels, issueLabel)
	if i.ClosedAt != nil {
		at := i.ClosedAt.UTC()
		out.ClosedAt = &at
	}
	return out
}

// issueTag is an issue's ETag: its version, then its organization's issue key, then the number and state of the task
// it shows and a digest of the branch it shows, quoted. The key is in it because a changed key renames the issue
// without changing its version, and a client holding the old name must read the new; the task and the branch, because
// they move on without changing the issue.
func issueTag(i issues.Issue) string {
	tag := strconv.FormatInt(i.Version, 10) + "-" + i.Key
	if i.Task != nil {
		tag += "-" + strconv.FormatInt(i.Task.Number, 10) + "-" + string(i.Task.State)
	}
	if b := i.Branch; b != nil {
		h := fnv.New32a()
		_, _ = h.Write([]byte(b.Repository.ID.String() + "/" + b.Name)) // a hash's Write never fails
		tag += "-b" + strconv.FormatUint(uint64(h.Sum32()), 36)
	}
	return `"` + tag + `"`
}

// issueMatch is the version of an issue that If-Match names, by the version its ETag starts with: 428 without one,
// 412 for one that names none. A change made from before a key change still applies: it changes the issue, not its
// name.
func issueMatch(header *string) (int64, error) {
	if header == nil || strings.TrimSpace(*header) == "" {
		return 0, refuse(http.StatusPreconditionRequired, "precondition.required", "Name the issue's ETag in If-Match.")
	}
	tag := strings.TrimSpace(*header)
	if tag == "*" {
		return 0, nil
	}
	number, _, _ := strings.Cut(strings.Trim(tag, `"`), "-")
	version, err := strconv.ParseInt(number, 10, 64)
	if err != nil || version < 1 {
		return 0, refuse(http.StatusPreconditionFailed, "precondition.failed", "If-Match names no version of the issue.")
	}
	return version, nil
}

// failIssue maps the issues domain's answers, then fail's.
func failIssue(err error) error {
	switch {
	case errors.Is(err, issues.ErrNotFound):
		return refuse(http.StatusNotFound, "issue.not_found", "There is no such issue, or you may not see it.")
	case errors.Is(err, issues.ErrChanged):
		return refuse(http.StatusPreconditionFailed, "precondition.failed", "The issue changed since you read it: read it again.")
	case errors.Is(err, issues.ErrBranchNotFound):
		return refuse(http.StatusNotFound, "branch.not_found", "There is no such branch, or you may not see it.")
	}
	return fail(err, "organization")
}

// ListIssues lists a page of the organization's issues, and links the next, or 304 while the caller holds that page,
// after waiting for it to change as wait asks.
func (a api) ListIssues(ctx context.Context, req ListIssuesRequestObject) (ListIssuesResponseObject, error) {
	v, org, waits, err := a.orgList(ctx, req.Org, req.Params.Wait, req.Params.IfNoneMatch)
	if err != nil {
		return nil, err
	}
	f, err := issueFilter(req.Params)
	if err != nil {
		return nil, err
	}
	return readList(ctx, waits, func(ctx context.Context) (issues.Page, error) {
		page, err := a.issues.List(ctx, v, org.ID, f)
		return page, failIssue(err)
	}, func(ctx context.Context) (<-chan issues.Page, error) {
		pages, err := a.issues.FollowList(ctx, v, org.ID, f)
		return pages, failIssue(err)
	}, func(page issues.Page) (ListIssuesResponseObject, bool, error) {
		out := ListIssues200JSONResponse{}
		out.Body.Issues = mapAll(page.Issues, issueOf)
		out.Body.Counts = IssueCounts{Open: page.Open, Closed: page.Closed}
		if page.Next != "" {
			link := `<?` + nextIssues(req.Params, page.Next).Encode() + `>; rel="next"`
			out.Headers.Link = &link
		}
		etag, err := bodyTag("issues", out.Body, out.Headers.Link)
		if err != nil {
			return nil, false, err
		}
		if matches(req.Params.IfNoneMatch, etag) {
			return ListIssues304Response{Headers: ListIssues304ResponseHeaders{ETag: etag}}, true, nil
		}
		out.Headers.ETag = etag
		return out, false, nil
	})
}

// issueFilter is the filter a list's parameters name, or 400.
func issueFilter(p ListIssuesParams) (issues.Filter, error) {
	var f issues.Filter
	if p.State != nil {
		for _, s := range *p.State {
			f.States = append(f.States, issues.State(s))
		}
	}
	var err error
	if f.AssigneeID, err = idOrNone("assignee", p.Assignee); err != nil {
		return f, err
	}
	if f.RepositoryID, err = idOrNone("repository", p.Repository); err != nil {
		return f, err
	}
	if p.Label != nil {
		f.LabelIDs = *p.Label
	}
	if p.Text != nil {
		f.Text = *p.Text
	}
	if p.Cursor != nil {
		f.Cursor = *p.Cursor
	}
	if p.Limit != nil {
		f.Limit = *p.Limit
		if f.Limit == 0 {
			return f, refuse(http.StatusBadRequest, "limit.invalid", "A page holds 1 to 100 issues.")
		}
	}
	return f, nil
}

// idOrNone is the ID a parameter names, uuid.Nil for none, or nil without one.
func idOrNone(name string, value *string) (*uuid.UUID, error) {
	if value == nil {
		return nil, nil
	}
	if *value == "none" {
		return &uuid.Nil, nil
	}
	id, err := uuid.Parse(*value)
	if err != nil {
		return nil, refuse(http.StatusBadRequest, name+".invalid", "Name the "+name+" by ID, or none.")
	}
	return &id, nil
}

// nextIssues is the query of the page after the one p asked for, which starts at cursor.
func nextIssues(p ListIssuesParams, cursor string) url.Values {
	next := url.Values{"cursor": {cursor}}
	if p.State != nil {
		for _, s := range *p.State {
			next.Add("state", string(s))
		}
	}
	if p.Label != nil {
		for _, id := range *p.Label {
			next.Add("label", id.String())
		}
	}
	for name, value := range map[string]*string{"assignee": p.Assignee, "repository": p.Repository, "q": p.Text} {
		if value != nil {
			next.Set(name, *value)
		}
	}
	if p.Limit != nil {
		next.Set("limit", strconv.Itoa(*p.Limit))
	}
	return next
}

// CreateIssue creates an issue in the organization.
func (a api) CreateIssue(ctx context.Context, req CreateIssueRequestObject) (CreateIssueResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	b := req.Body
	in := issues.Input{Title: b.Title}
	if b.Description != nil {
		in.Description = *b.Description
	}
	if b.State != nil {
		in.State = issues.State(*b.State)
	}
	if b.Priority != nil {
		if in.Priority, err = issues.ParsePriority(string(*b.Priority)); err != nil {
			return nil, fail(err, "issue")
		}
	}
	if b.AssigneeId != nil {
		in.AssigneeID = *b.AssigneeId
	}
	if b.RepositoryId != nil {
		in.RepositoryID = *b.RepositoryId
	}
	if b.LabelIds != nil {
		in.LabelIDs = *b.LabelIds
	}
	created, err := a.issues.Create(ctx, v, org.ID, in)
	if err != nil {
		return nil, failIssue(err)
	}
	location := BaseURL + "/orgs/" + url.PathEscape(org.Slug) + "/issues/" + strconv.Itoa(created.Number)
	return CreateIssue201JSONResponse{Body: issueOf(created), Headers: CreateIssue201ResponseHeaders{ETag: issueTag(created), Location: location}}, nil
}

// GetIssue answers one of the organization's issues, or 304 while the caller holds it, after waiting for it to change
// as wait asks.
func (a api) GetIssue(ctx context.Context, req GetIssueRequestObject) (GetIssueResponseObject, error) {
	v, org, waits, err := a.orgList(ctx, req.Org, req.Params.Wait, req.Params.IfNoneMatch)
	if err != nil {
		return nil, err
	}
	return readList(ctx, waits, func(ctx context.Context) (issues.Issue, error) {
		issue, err := a.issues.Get(ctx, v, org.ID, req.Number)
		return issue, failIssue(err)
	}, func(ctx context.Context) (<-chan issues.Issue, error) {
		issue, err := a.issues.FollowIssue(ctx, v, org.ID, req.Number)
		return issue, failIssue(err)
	}, func(issue issues.Issue) (GetIssueResponseObject, bool, error) {
		tag := issueTag(issue)
		if matches(req.Params.IfNoneMatch, tag) {
			return GetIssue304Response{Headers: GetIssue304ResponseHeaders{ETag: tag}}, true, nil
		}
		return GetIssue200JSONResponse{Body: issueOf(issue), Headers: GetIssue200ResponseHeaders{ETag: tag}}, false, nil
	})
}

// UpdateIssue changes the fields the merge patch names.
func (a api) UpdateIssue(ctx context.Context, req UpdateIssueRequestObject) (UpdateIssueResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	version, err := issueMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	p, err := issuePatch(*req.Body)
	if err != nil {
		return nil, fail(err, "issue")
	}
	p.Version = version
	updated, err := a.issues.Update(ctx, v, org.ID, req.Number, p)
	if err != nil {
		return nil, failIssue(err)
	}
	return UpdateIssue200JSONResponse{Body: issueOf(updated), Headers: UpdateIssue200ResponseHeaders{ETag: issueTag(updated)}}, nil
}

// issuePatch is the domain's patch of a merge patch: null clears an assignee or a repository.
func issuePatch(b IssuePatch) (issues.Patch, error) {
	p := issues.Patch{Title: b.Title, Description: b.Description}
	if b.State != nil {
		s := issues.State(*b.State)
		p.State = &s
	}
	if b.Priority != nil {
		priority, err := issues.ParsePriority(string(*b.Priority))
		if err != nil {
			return p, fmt.Errorf("reading the priority: %w", err)
		}
		p.Priority = &priority
	}
	p.AssigneeID, p.RepositoryID, p.LabelIDs = idOrClear(b.AssigneeId), idOrClear(b.RepositoryId), b.LabelIds
	return p, nil
}

// idOrClear is the ID a merge patch's field names, uuid.Nil for null, or nil when it is left out.
func idOrClear(field nullable.Nullable[uuid.UUID]) *uuid.UUID {
	switch {
	case !field.IsSpecified():
		return nil
	case field.IsNull():
		return &uuid.Nil
	}
	id := field.MustGet()
	return &id
}

// DeleteIssue deletes one of the organization's issues.
func (a api) DeleteIssue(ctx context.Context, req DeleteIssueRequestObject) (DeleteIssueResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	version, err := issueMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.issues.Delete(ctx, v, org.ID, req.Number, version); err != nil {
		return nil, failIssue(err)
	}
	return DeleteIssue204Response{}, nil
}

// ListIssueActivity lists what happened to one of the organization's issues, or 304 while the caller holds that list,
// after waiting for more to happen as wait asks.
func (a api) ListIssueActivity(ctx context.Context, req ListIssueActivityRequestObject) (ListIssueActivityResponseObject, error) {
	v, org, waits, err := a.orgList(ctx, req.Org, req.Params.Wait, req.Params.IfNoneMatch)
	if err != nil {
		return nil, err
	}
	return readList(ctx, waits, func(ctx context.Context) ([]issues.Event, error) {
		events, err := a.issues.Activity(ctx, v, org.ID, req.Number)
		return events, failIssue(err)
	}, func(ctx context.Context) (<-chan []issues.Event, error) {
		events, err := a.issues.FollowActivity(ctx, v, org.ID, req.Number)
		return events, failIssue(err)
	}, func(events []issues.Event) (ListIssueActivityResponseObject, bool, error) {
		out := ListIssueActivity200JSONResponse{}
		out.Body.Events = mapAll(events, issueEvent)
		return listAnswer("issue activity", out.Body, req.Params.IfNoneMatch,
			func(etag string) ListIssueActivityResponseObject { out.Headers.ETag = etag; return out },
			func(etag string) ListIssueActivityResponseObject {
				return ListIssueActivity304Response{Headers: ListIssueActivity304ResponseHeaders{ETag: etag}}
			})
	})
}

func issueEvent(e issues.Event) IssueEvent {
	out := IssueEvent{Id: e.ID, Kind: e.Kind, At: e.At.UTC(), Changes: mapAll(e.Changes, issueChange)}
	if e.Actor != nil {
		out.Actor = &Principal{Id: e.Actor.ID, DisplayName: e.Actor.DisplayName}
	}
	if b := e.Branch; b != nil {
		out.Branch = &IssueEventBranch{LinkedBy: LinkedBy(b.LinkedBy)}
		if b.Name != "" {
			out.Branch.Repository, out.Branch.Name = &b.Repository, &b.Name
		}
	}
	if t := e.Task; t != nil {
		out.Task = &IssueEventTask{Number: t.Number}
		if t.Branch != "" {
			out.Task.Branch = &t.Branch
		}
	}
	return out
}

func issueChange(c issues.Change) IssueChange {
	out := IssueChange{Field: IssueChangeField(c.Field)}
	if c.From != "" {
		out.From = &c.From
	}
	if c.To != "" {
		out.To = &c.To
	}
	return out
}

// ListIssueBranches lists the branches linked to one of the organization's issues, or 304 while the caller holds that
// list, after waiting for it to change as wait asks.
func (a api) ListIssueBranches(ctx context.Context, req ListIssueBranchesRequestObject) (ListIssueBranchesResponseObject, error) {
	v, org, waits, err := a.orgList(ctx, req.Org, req.Params.Wait, req.Params.IfNoneMatch)
	if err != nil {
		return nil, err
	}
	return readList(ctx, waits, func(ctx context.Context) ([]issues.LinkedBranch, error) {
		branches, err := a.issues.Branches(ctx, v, org.ID, req.Number)
		return branches, failIssue(err)
	}, func(ctx context.Context) (<-chan []issues.LinkedBranch, error) {
		branches, err := a.issues.FollowBranches(ctx, v, org.ID, req.Number)
		return branches, failIssue(err)
	}, func(branches []issues.LinkedBranch) (ListIssueBranchesResponseObject, bool, error) {
		out := ListIssueBranches200JSONResponse{}
		out.Body.Branches = mapAll(branches, issueBranch)
		return listAnswer("issue branches", out.Body, req.Params.IfNoneMatch,
			func(etag string) ListIssueBranchesResponseObject { out.Headers.ETag = etag; return out },
			func(etag string) ListIssueBranchesResponseObject {
				return ListIssueBranches304Response{Headers: ListIssueBranches304ResponseHeaders{ETag: etag}}
			})
	})
}

func issueBranch(b issues.LinkedBranch) IssueBranch {
	out := IssueBranch{Repository: IssueRepository{Id: b.Repository.ID, Slug: b.Repository.Slug, Name: b.Repository.Name}, Name: b.Name,
		Head: b.Head, Deleted: b.Deleted, LinkedBy: LinkedBy(b.LinkedBy), LinkedAt: b.LinkedAt.UTC()}
	if b.Status != "" {
		status := BranchStatus(b.Status)
		out.Status = &status
	}
	return out
}

// branchIssue is a branch's link as the API shows it.
func branchIssue(l issues.Link) BranchIssue {
	return BranchIssue{Number: l.Issue.Number, Identifier: l.Issue.Identifier(), Title: l.Issue.Title, State: IssueState(l.Issue.State),
		Priority: IssuePriority(l.Issue.Priority.String()), LinkedBy: LinkedBy(l.LinkedBy)}
}

// LinkBranch links one of the repository's live branches to one of the organization's issues by hand.
func (a api) LinkBranch(ctx context.Context, req LinkBranchRequestObject) (LinkBranchResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, refuse(http.StatusBadRequest, "issue.invalid", "Name the issue's number.")
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	link, err := a.issues.Link(ctx, v, repo.OrgID, repo.ID, req.BranchName, req.Body.Number)
	if err != nil {
		return nil, failIssue(err)
	}
	return LinkBranch200JSONResponse(branchIssue(link)), nil
}

// UnlinkBranch unlinks one of the repository's live branches from its issue by hand.
func (a api) UnlinkBranch(ctx context.Context, req UnlinkBranchRequestObject) (UnlinkBranchResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	if err := a.issues.Unlink(ctx, v, repo.OrgID, repo.ID, req.BranchName); err != nil {
		return nil, failIssue(err)
	}
	return UnlinkBranch204Response{}, nil
}
