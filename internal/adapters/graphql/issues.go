package graphql

import (
	"context"
	"errors"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/issues"
)

// Issues is what the API needs of the issues' service, its organizations' labels included.
type Issues interface {
	Labels(ctx context.Context, v issues.Viewer, orgID uuid.UUID) ([]issues.Label, error)
	CreateLabel(ctx context.Context, v issues.Viewer, orgID uuid.UUID, in issues.LabelInput) (issues.Label, error)
	UpdateLabel(ctx context.Context, v issues.Viewer, orgID, labelID uuid.UUID, p issues.LabelPatch) (issues.Label, error)
	DeleteLabel(ctx context.Context, v issues.Viewer, orgID, labelID uuid.UUID, version int64) error
	List(ctx context.Context, v issues.Viewer, orgID uuid.UUID, f issues.Filter) (issues.Page, error)
	Create(ctx context.Context, v issues.Viewer, orgID uuid.UUID, in issues.Input) (issues.Issue, error)
	Get(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int) (issues.Issue, error)
	Update(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int, p issues.Patch) (issues.Issue, error)
	Delete(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int, version int64) error
	Activity(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int) ([]issues.Event, error)
	Branches(ctx context.Context, v issues.Viewer, orgID uuid.UUID, number int) ([]issues.LinkedBranch, error)
	OfBranch(ctx context.Context, v issues.Viewer, orgID, repoID uuid.UUID, name string) (*issues.Link, error)
	Link(ctx context.Context, v issues.Viewer, orgID, repoID uuid.UUID, name string, number int) (issues.Link, error)
	Unlink(ctx context.Context, v issues.Viewer, orgID, repoID uuid.UUID, name string) error
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
	if t := i.Task; t != nil {
		out.Task = &IssueTask{Number: int(t.Number), State: TaskState(strings.ToUpper(string(t.State)))}
	}
	if b := i.Branch; b != nil {
		out.Branch = &IssueLiveBranch{RepositoryID: b.Repository.ID.String(), RepositorySlug: b.Repository.Slug, Name: b.Name}
	}
	out.Labels = mapAll(i.Labels, func(l issues.IssueLabel) *IssueLabel {
		return &IssueLabel{ID: l.ID.String(), Name: l.Name, Color: labelColors[l.Color]}
	})
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
	if b := e.Branch; b != nil {
		out.Branch = &IssueEventBranch{LinkedBy: linkedBy[b.LinkedBy]}
		if b.Name != "" {
			out.Branch.Repository, out.Branch.Name = &b.Repository, &b.Name
		}
	}
	if t := e.Task; t != nil {
		out.Task = &IssueEventTask{Number: int(t.Number)}
		if t.Branch != "" {
			out.Task.Branch = &t.Branch
		}
	}
	return out
}

// linkedBy maps the domain's ways of linking a branch to the schema's.
var linkedBy = map[issues.LinkedBy]LinkedBy{issues.LinkedByName: LinkedByName, issues.LinkedByHand: LinkedByHand}

// branchIssue is a branch's link as the API shows it.
func branchIssue(l issues.Link) *BranchIssue {
	return &BranchIssue{Issue: issueOf(l.Issue), LinkedBy: linkedBy[l.LinkedBy]}
}

func issueBranch(b issues.LinkedBranch) *IssueBranch {
	out := &IssueBranch{Repository: &IssueRepository{ID: b.Repository.ID.String(), Slug: b.Repository.Slug, Name: b.Repository.Name}, Name: b.Name,
		Head: b.Head, Deleted: b.Deleted, LinkedBy: linkedBy[b.LinkedBy], LinkedAt: b.LinkedAt.UTC()}
	if b.Status != "" {
		status := branchStatuses[code.BranchStatus(b.Status)]
		out.Status = &status
	}
	return out
}

// issueFail is fail for issues: NOT_FOUND for an issue, a branch or a label that isn't there, CONFLICT for an issue or
// a label that changed and for a label's name that another has.
func issueFail(ctx context.Context, err error, what string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, issues.ErrNotFound):
		return refuse(ctx, notFound, "issue.not_found", "there is no such issue, or you may not see it")
	case errors.Is(err, issues.ErrBranchNotFound):
		return refuse(ctx, notFound, "branch.not_found", "there is no such branch, or you may not see it")
	case errors.Is(err, issues.ErrChanged):
		return refuse(ctx, conflict, "precondition.failed", "the issue changed since you read it: read it again")
	case errors.Is(err, issues.ErrLabelNotFound):
		return refuse(ctx, notFound, "label.not_found", "there is no such label, or you may not see it")
	case errors.Is(err, issues.ErrLabelChanged):
		return refuse(ctx, conflict, "precondition.failed", "the label changed since you read it: read it again")
	case errors.Is(err, issues.ErrLabelTaken):
		return refuse(ctx, conflict, "label.taken", "the organization has a label of that name")
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
	if f.LabelIDs, err = ids(ctx, "labelIds", filter.LabelIds); err != nil {
		return f, err
	}
	f.RepositoryID, err = idOrNone(ctx, "repository", filter.RepositoryID)
	return f, err
}

// ids are the IDs a list names, or BAD_USER_INPUT for one that isn't an ID.
func ids(ctx context.Context, field string, values []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(values))
	for _, v := range values {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, refuse(ctx, badUserInput, field+".invalid", "not an ID")
		}
		out = append(out, id)
	}
	return out, nil
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
	var err error
	in.LabelIDs, err = ids(ctx, "labelIds", input.LabelIds)
	return in, err
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
	if input.LabelIds != nil {
		labels, err := ids(ctx, "labelIds", input.LabelIds)
		if err != nil {
			return p, err
		}
		p.LabelIDs = &labels
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

// labelColors maps the domain's colours to the schema's.
var labelColors = map[issues.Color]LabelColor{issues.ColorGray: LabelColorGray, issues.ColorBlue: LabelColorBlue, issues.ColorGreen: LabelColorGreen,
	issues.ColorYellow: LabelColorYellow, issues.ColorRed: LabelColorRed}

// colorOf is the domain's colour of the schema's.
func colorOf(c LabelColor) issues.Color {
	for domain, schema := range labelColors {
		if schema == c {
			return domain
		}
	}
	return issues.Color(c)
}

// labelOf is l as the API shows it.
func labelOf(l issues.Label) *Label {
	return &Label{ID: l.ID.String(), Name: l.Name, Color: labelColors[l.Color], Description: l.Description, Issues: l.Issues,
		CreatedAt: l.CreatedAt.UTC(), UpdatedAt: l.UpdatedAt.UTC(), Version: int(l.Version)}
}

// labelOrg is the viewer, the organization and the label a label's change names, and the version it expects, from 1,
// or 0 for whichever is there.
func labelOrg(ctx context.Context, organizationID, id string, expectedVersion *int) (issues.Viewer, uuid.UUID, uuid.UUID, int64, error) {
	v, orgID, err := issueOrg(ctx, organizationID)
	if err != nil {
		return issues.Viewer{}, uuid.Nil, uuid.Nil, 0, err
	}
	labelID, err := parseID(ctx, id, "label")
	if err != nil {
		return issues.Viewer{}, uuid.Nil, uuid.Nil, 0, err
	}
	if expectedVersion == nil {
		return v, orgID, labelID, 0, nil
	}
	if *expectedVersion < 1 {
		return issues.Viewer{}, uuid.Nil, uuid.Nil, 0, refuse(ctx, badUserInput, "version.invalid", "a version of the label, from 1")
	}
	return v, orgID, labelID, int64(*expectedVersion), nil
}
