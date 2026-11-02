package graphql

import (
	"context"
	"errors"

	"github.com/99designs/gqlgen/graphql"
	"github.com/google/uuid"

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

// issueStates maps the domain's states to the schema's.
var issueStates = map[issues.State]IssueState{
	issues.StateBacklog: IssueStateBacklog, issues.StateTodo: IssueStateTodo, issues.StateInProgress: IssueStateInProgress,
	issues.StateInReview: IssueStateInReview, issues.StateDone: IssueStateDone, issues.StateCanceled: IssueStateCanceled,
}

// issuePriorities maps the domain's priorities to the schema's, by number.
var issuePriorities = []IssuePriority{IssuePriorityNone, IssuePriorityUrgent, IssuePriorityHigh, IssuePriorityMedium, IssuePriorityLow}

// stateOf is the domain's state of the schema's.
func stateOf(s IssueState) issues.State {
	for domain, schema := range issueStates {
		if schema == s {
			return domain
		}
	}
	return issues.State(s)
}

// priorityOf is the domain's priority of the schema's.
func priorityOf(p IssuePriority) issues.Priority {
	for i, schema := range issuePriorities {
		if schema == p {
			return issues.Priority(i)
		}
	}
	return issues.Priority(-1)
}

func issuePrincipal(p *issues.Principal) *IssuePrincipal {
	if p == nil {
		return nil
	}
	return &IssuePrincipal{ID: p.ID.String(), DisplayName: p.DisplayName}
}

// issueOf is i as the API shows it.
func issueOf(i issues.Issue) *Issue {
	out := &Issue{ID: i.ID.String(), OrganizationID: i.OrgID.String(), Number: i.Number, Identifier: i.Identifier(), Title: i.Title,
		Description: i.Description, State: issueStates[i.State], Priority: issuePriorities[i.Priority], Assignee: issuePrincipal(i.Assignee),
		CreatedBy: issuePrincipal(i.CreatedBy), CreatedAt: i.CreatedAt.UTC(), UpdatedAt: i.UpdatedAt.UTC(), Version: int(i.Version)}
	if i.DescriptionHTML != "" {
		out.DescriptionHTML = &i.DescriptionHTML
	}
	if r := i.Repository; r != nil {
		out.Repository = &IssueRepository{ID: r.ID.String(), Slug: r.Slug, Name: r.Name}
	}
	if i.ClosedAt != nil {
		at := i.ClosedAt.UTC()
		out.ClosedAt = &at
	}
	return out
}

func issueEvent(e issues.Event) *IssueEvent {
	out := &IssueEvent{ID: e.ID.String(), Kind: e.Kind, Actor: issuePrincipal(e.Actor), At: e.At.UTC(), Changes: make([]*IssueChange, 0, len(e.Changes))}
	for _, c := range e.Changes {
		change := &IssueChange{Field: c.Field}
		if c.From != "" {
			change.From = &c.From
		}
		if c.To != "" {
			change.To = &c.To
		}
		out.Changes = append(out.Changes, change)
	}
	return out
}

// issueFail is fail for issues: NOT_FOUND for an issue that isn't there, CONFLICT for one that changed.
func issueFail(ctx context.Context, err error, what string) error {
	switch {
	case errors.Is(err, issues.ErrNotFound):
		return refuse(ctx, notFound, "issue.not_found", "there is no such issue, or you may not see it")
	case errors.Is(err, issues.ErrChanged):
		return refuse(ctx, conflict, "precondition.failed", "the issue changed since you read it: read it again")
	}
	return fail(ctx, err, "organization", what)
}

// idOrNone is the ID a filter names, uuid.Nil for none, or nil without one.
func idOrNone(ctx context.Context, field string, value *string) (*uuid.UUID, error) {
	if value == nil {
		return nil, nil
	}
	if *value == "none" {
		return &uuid.Nil, nil
	}
	id, err := uuid.Parse(*value)
	if err != nil {
		return nil, refuse(ctx, badUserInput, field+".invalid", "name the "+field+" by ID, or none")
	}
	return &id, nil
}

// issueFilter is filter as the domain takes it.
func issueFilter(ctx context.Context, filter *IssueFilter, first int, after *string) (issues.Filter, error) {
	f := issues.Filter{Limit: first}
	if first == 0 {
		return f, refuse(ctx, badUserInput, "limit.invalid", "a page holds 1 to 100 issues")
	}
	if after != nil {
		f.Cursor = *after
	}
	if filter == nil {
		return f, nil
	}
	for _, s := range filter.States {
		f.States = append(f.States, stateOf(s))
	}
	if filter.Text != nil {
		f.Text = *filter.Text
	}
	var err error
	if f.AssigneeID, err = idOrNone(ctx, "assignee", filter.AssigneeID); err != nil {
		return f, err
	}
	f.RepositoryID, err = idOrNone(ctx, "repository", filter.RepositoryID)
	return f, err
}

// issueInput is input as the domain takes it.
func issueInput(ctx context.Context, input IssueInput) (issues.Input, error) {
	in := issues.Input{Title: input.Title}
	if input.Description != nil {
		in.Description = *input.Description
	}
	if input.State != nil {
		in.State = stateOf(*input.State)
	}
	if input.Priority != nil {
		in.Priority = priorityOf(*input.Priority)
	}
	for _, id := range []struct {
		field string
		value *string
		into  *uuid.UUID
	}{{"assigneeId", input.AssigneeID, &in.AssigneeID}, {"repositoryId", input.RepositoryID, &in.RepositoryID}} {
		if id.value == nil {
			continue
		}
		parsed, err := uuid.Parse(*id.value)
		if err != nil {
			return in, refuse(ctx, badUserInput, id.field+".invalid", "not an ID")
		}
		*id.into = parsed
	}
	return in, nil
}

// issuePatch is input as the domain takes it: null clears an assignee or a repository.
func issuePatch(ctx context.Context, input IssuePatch) (issues.Patch, error) {
	p := issues.Patch{Title: input.Title, Description: input.Description}
	if input.State != nil {
		s := stateOf(*input.State)
		p.State = &s
	}
	if input.Priority != nil {
		priority := priorityOf(*input.Priority)
		p.Priority = &priority
	}
	var err error
	if p.AssigneeID, err = idOrClear(ctx, "assigneeId", input.AssigneeID); err != nil {
		return p, err
	}
	p.RepositoryID, err = idOrClear(ctx, "repositoryId", input.RepositoryID)
	return p, err
}

// idOrClear is the ID an omittable field names, uuid.Nil for null, or nil when it is left out.
func idOrClear(ctx context.Context, field string, value graphql.Omittable[*string]) (*uuid.UUID, error) {
	v, set := value.ValueOK()
	switch {
	case !set:
		return nil, nil
	case v == nil:
		return &uuid.Nil, nil
	}
	id, err := uuid.Parse(*v)
	if err != nil {
		return nil, refuse(ctx, badUserInput, field+".invalid", "not an ID")
	}
	return &id, nil
}

// issueVersion is the version expectedVersion names, from 1, or 0 without one.
func issueVersion(ctx context.Context, expectedVersion *int) (int64, error) {
	if expectedVersion == nil {
		return 0, nil
	}
	if *expectedVersion < 1 {
		return 0, refuse(ctx, badUserInput, "version.invalid", "a version of the issue, from 1")
	}
	return int64(*expectedVersion), nil
}

// issueOrg is the viewer and the organization an issue's operation names.
func issueOrg(ctx context.Context, organizationID string) (issues.Viewer, uuid.UUID, error) {
	v, err := viewer(ctx)
	if err != nil {
		return issues.Viewer{}, uuid.Nil, err
	}
	orgID, err := parseID(ctx, organizationID, "organization")
	if err != nil {
		return issues.Viewer{}, uuid.Nil, err
	}
	return v.User, orgID, nil
}
