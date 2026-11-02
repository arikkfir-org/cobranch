package rest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"

	"cobranch.dev/internal/services/issues"
)

// Issues is what the API needs of the issues' service.
type Issues interface {
	List(ctx context.Context, v issues.Viewer, orgID uuid.UUID, f issues.Filter) (issues.Page, error)
	Create(ctx context.Context, v issues.Viewer, orgID uuid.UUID, in issues.Input) (issues.Issue, error)
	Get(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int) (issues.Issue, error)
	Update(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int, p issues.Patch) (issues.Issue, error)
	Delete(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int, version int64) error
	Activity(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int) ([]issues.Event, error)
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
	if i.CreatedBy != nil {
		out.CreatedBy = &Principal{Id: i.CreatedBy.ID, DisplayName: i.CreatedBy.DisplayName}
	}
	if r := i.Repository; r != nil {
		out.Repository = &IssueRepository{Id: r.ID, Slug: r.Slug, Name: r.Name}
	}
	if i.ClosedAt != nil {
		at := i.ClosedAt.UTC()
		out.ClosedAt = &at
	}
	return out
}

// issueTag is an issue's ETag: its version, then its organization's issue key, quoted. The key is in it because a
// changed key renames the issue without changing its version, and a client holding the old name must read the new.
func issueTag(i issues.Issue) string {
	return `"` + strconv.FormatInt(i.Version, 10) + "-" + i.Key + `"`
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
	}
	return fail(err, "organization")
}

// ListIssues lists a page of the organization's issues, and links the next.
func (a api) ListIssues(ctx context.Context, req ListIssuesRequestObject) (ListIssuesResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	f, err := issueFilter(req.Params)
	if err != nil {
		return nil, err
	}
	page, err := a.issues.List(ctx, v, org.ID, f)
	if err != nil {
		return nil, failIssue(err)
	}
	out := ListIssues200JSONResponse{}
	out.Body.Issues = mapAll(page.Issues, issueOf)
	if page.Next != "" {
		link := `<?` + nextIssues(req.Params, page.Next).Encode() + `>; rel="next"`
		out.Headers.Link = &link
	}
	return out, nil
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
	created, err := a.issues.Create(ctx, v, org.ID, in)
	if err != nil {
		return nil, failIssue(err)
	}
	location := BaseURL + "/orgs/" + url.PathEscape(org.Slug) + "/issues/" + strconv.Itoa(created.Number)
	return CreateIssue201JSONResponse{Body: issueOf(created), Headers: CreateIssue201ResponseHeaders{ETag: issueTag(created), Location: location}}, nil
}

// GetIssue answers one of the organization's issues, or 304 while the caller holds it.
func (a api) GetIssue(ctx context.Context, req GetIssueRequestObject) (GetIssueResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	issue, err := a.issues.Get(ctx, v, org.ID, req.Number)
	if err != nil {
		return nil, failIssue(err)
	}
	tag := issueTag(issue)
	if matches(req.Params.IfNoneMatch, tag) {
		return GetIssue304Response{Headers: GetIssue304ResponseHeaders{ETag: tag}}, nil
	}
	return GetIssue200JSONResponse{Body: issueOf(issue), Headers: GetIssue200ResponseHeaders{ETag: tag}}, nil
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
	p.AssigneeID, p.RepositoryID = idOrClear(b.AssigneeId), idOrClear(b.RepositoryId)
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

// ListIssueActivity lists what happened to one of the organization's issues.
func (a api) ListIssueActivity(ctx context.Context, req ListIssueActivityRequestObject) (ListIssueActivityResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	events, err := a.issues.Activity(ctx, v, org.ID, req.Number)
	if err != nil {
		return nil, failIssue(err)
	}
	return ListIssueActivity200JSONResponse{Events: mapAll(events, issueEvent)}, nil
}

func issueEvent(e issues.Event) IssueEvent {
	out := IssueEvent{Id: e.ID, Kind: e.Kind, At: e.At.UTC(), Changes: mapAll(e.Changes, issueChange)}
	if e.Actor != nil {
		out.Actor = &Principal{Id: e.Actor.ID, DisplayName: e.Actor.DisplayName}
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
