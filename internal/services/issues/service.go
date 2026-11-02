package issues

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

var tracer = otel.Tracer("cobranch.dev/internal/services/issues")

// Store is what the database keeps of issues. Each write is one transaction that acts for actor, so the audit log
// records who changed each row, and appends the issue's event.
type Store interface {
	// Create numbers the issue in its organization, records it and its event issue.created, and answers it as kept.
	Create(ctx context.Context, actor uuid.UUID, issue Issue) (Issue, error)
	// Get finds one of the organization's issues by number, or ErrNotFound.
	Get(ctx context.Context, orgID uuid.UUID, number int) (Issue, error)
	// List lists a page of the organization's issues that q matches, newest first, and whether more follow.
	List(ctx context.Context, orgID uuid.UUID, q Query) ([]Issue, bool, error)
	// Update writes issue's fields over the one at version, records event issue.changed with its changes, and answers
	// it as kept, or ErrChanged when it is at another version now, or ErrNotFound when it is gone.
	Update(ctx context.Context, actor uuid.UUID, issue Issue, version int64, changes []Change) (Issue, error)
	// Delete deletes the issue at version with its events and records event issue.deleted at the organization, or
	// ErrChanged when it is at another version now, or ErrNotFound when it is gone.
	Delete(ctx context.Context, actor uuid.UUID, issue Issue, version int64) error
	// Activity lists the issue's events, oldest first.
	Activity(ctx context.Context, issueID uuid.UUID) ([]Event, error)
	// Member finds one of the organization's members, whom an issue may be assigned to, or ErrNotFound.
	Member(ctx context.Context, orgID, principalID uuid.UUID) (Principal, error)
	// Repository finds one of the organization's repositories, or ErrNotFound.
	Repository(ctx context.Context, orgID, repoID uuid.UUID) (Repository, error)
}

// Renderer renders an issue's description, as the code browsing design renders a repository's Markdown.
type Renderer = code.Renderer

// The permissions issues take, at the organization.
const (
	// PermRead reads the organization's issues and their activity.
	PermRead = "issues.read"
	// PermWrite creates issues and changes any of their fields.
	PermWrite = "issues.write"
	// PermManage deletes issues.
	PermManage = "issues.manage"
)

// Service runs an organization's issues: every operation checks its viewer's permissions through the authorizer
// first.
type Service struct {
	store    Store
	markdown Renderer
	authz    *auth.Authorizer
	now      func() time.Time
}

// NewService returns a Service over store, rendering descriptions with markdown and checking permissions with authz.
// Without markdown, a description answers as text alone.
func NewService(store Store, markdown Renderer, authz *auth.Authorizer) *Service {
	return &Service{store: store, markdown: markdown, authz: authz, now: time.Now}
}

// SetClock makes the service read the time from now: for tests.
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

// Viewer is who an operation runs for: a signed-in user, or an API key.
type Viewer = auth.User

// require checks that v holds permission at the organization: orgs.ErrNotFound without org.read, so nobody learns
// that an organization they can't see exists, and auth.ErrForbidden without the permission.
func (s *Service) require(ctx context.Context, v Viewer, orgID uuid.UUID, permission string) error {
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeOrganization, orgID)
	if err != nil {
		return fmt.Errorf("reading the viewer's permissions: %w", err)
	}
	switch {
	case !slices.Contains(held, orgs.PermRead):
		return orgs.ErrNotFound
	case !slices.Contains(held, permission):
		return fmt.Errorf("%w: %s at the organization", auth.ErrForbidden, permission)
	}
	return nil
}

// readable lists the organization's repositories v reads.
func (s *Service) readable(ctx context.Context, v Viewer, orgID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := s.authz.Repositories(ctx, v.ID, orgID, repos.PermRead)
	if err != nil {
		return nil, fmt.Errorf("listing the repositories the viewer reads: %w", err)
	}
	return ids, nil
}

// shown is issue as v sees it: without its repository unless v reads it.
func shown(issue Issue, readable []uuid.UUID) Issue {
	if issue.Repository != nil && !slices.Contains(readable, issue.Repository.ID) {
		issue.Repository = nil
	}
	return issue
}

// List lists a page of the organization's issues that f matches, newest first; it takes issues.read. Filtering by a
// repository v doesn't read finds none.
func (s *Service) List(ctx context.Context, v Viewer, orgID uuid.UUID, f Filter) (Page, error) {
	ctx, span := tracer.Start(ctx, "issues.Service.List")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()))
	if err := s.require(ctx, v, orgID, PermRead); err != nil {
		return Page{}, failed(span, "listing the issues", err)
	}
	q, err := query(f)
	if err != nil {
		return Page{}, err
	}
	readable, err := s.readable(ctx, v, orgID)
	if err != nil {
		return Page{}, failed(span, "listing the issues", err)
	}
	if q.RepositoryID != nil && *q.RepositoryID != uuid.Nil && !slices.Contains(readable, *q.RepositoryID) {
		return Page{}, nil
	}
	list, more, err := s.store.List(ctx, orgID, q)
	if err != nil {
		return Page{}, failed(span, "listing the issues", err)
	}
	page := Page{Issues: make([]Issue, 0, len(list))}
	for _, i := range list {
		page.Issues = append(page.Issues, shown(i, readable))
	}
	if more && len(list) > 0 {
		page.Next = cursor(list[len(list)-1].Number)
	}
	return page, nil
}

// query is f as the store runs it, or a *orgs.ValidationError.
func query(f Filter) (Query, error) {
	q := Query{States: f.States, AssigneeID: f.AssigneeID, RepositoryID: f.RepositoryID, Limit: f.Limit}
	if len(q.States) == 0 {
		q.States = OpenStates
	}
	for _, st := range q.States {
		if err := ValidateState("state", st); err != nil {
			return Query{}, err
		}
	}
	switch {
	case q.Limit == 0:
		q.Limit = DefaultPageSize
	case q.Limit < 0 || q.Limit > MaxPageSize:
		return Query{}, &orgs.ValidationError{Field: "limit", Code: "limit.invalid", Message: "a page holds 1 to 100 issues"}
	}
	below, err := decodeCursor(f.Cursor)
	if err != nil {
		return Query{}, err
	}
	q.Below = below
	if len(f.Text) > MaxTitle {
		return Query{}, &orgs.ValidationError{Field: "text", Code: "text.invalid", Message: "at most 200 characters"}
	}
	q.Text = f.Text
	if n, ok := ParseIdentifier(f.Text); ok {
		q.Number = n
	}
	return q, nil
}

// Get finds one of the organization's issues by number, with its description rendered; it takes issues.read.
func (s *Service) Get(ctx context.Context, v Viewer, orgID uuid.UUID, number int) (Issue, error) {
	ctx, span := tracer.Start(ctx, "issues.Service.Get")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()), attribute.Int("cobranch.issue.number", number))
	issue, err := s.get(ctx, v, orgID, number, PermRead)
	if err != nil {
		return Issue{}, failed(span, "reading the issue", err)
	}
	return s.rendered(ctx, issue)
}

// get finds the issue for v, who holds permission at its organization, as v sees it.
func (s *Service) get(ctx context.Context, v Viewer, orgID uuid.UUID, number int, permission string) (Issue, error) {
	if err := s.require(ctx, v, orgID, permission); err != nil {
		return Issue{}, err
	}
	issue, err := s.store.Get(ctx, orgID, number)
	if err != nil {
		return Issue{}, wrap("finding the issue", err)
	}
	readable, err := s.readable(ctx, v, orgID)
	if err != nil {
		return Issue{}, err
	}
	return shown(issue, readable), nil
}

// rendered is issue with its description rendered, its relative links pointing at the organization's Console pages
// beside the issue's; as text alone without a renderer.
func (s *Service) rendered(ctx context.Context, issue Issue) (Issue, error) {
	if s.markdown == nil || issue.Description == "" {
		return issue, nil
	}
	html, err := s.markdown.Render(ctx, []byte(issue.Description), code.Document{Org: issue.OrgSlug, Path: "issues/" + issue.Identifier()})
	if err != nil {
		return Issue{}, fmt.Errorf("rendering the description of %s: %w", issue.Identifier(), err)
	}
	issue.DescriptionHTML = html
	return issue, nil
}

// Create creates an issue in the organization, numbered after its last, created by v; it takes issues.write, and
// repo.read on the repository it is about.
func (s *Service) Create(ctx context.Context, v Viewer, orgID uuid.UUID, in Input) (Issue, error) {
	ctx, span := tracer.Start(ctx, "issues.Service.Create")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()))
	if err := s.require(ctx, v, orgID, PermWrite); err != nil {
		return Issue{}, failed(span, "creating the issue", err)
	}
	in, err := Validate(in)
	if err != nil {
		return Issue{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Issue{}, fmt.Errorf("making an ID: %w", err)
	}
	now := s.now()
	issue := Issue{ID: id, OrgID: orgID, Title: in.Title, Description: in.Description, State: in.State, Priority: in.Priority,
		CreatedBy: &Principal{ID: v.ID}, CreatedAt: now, UpdatedAt: now, Version: 1}
	if in.State.Closed() {
		issue.ClosedAt = &now
	}
	if issue.Assignee, err = s.assignee(ctx, orgID, in.AssigneeID); err != nil {
		return Issue{}, failed(span, "creating the issue", err)
	}
	if issue.Repository, err = s.repository(ctx, v, orgID, in.RepositoryID); err != nil {
		return Issue{}, failed(span, "creating the issue", err)
	}
	created, err := s.store.Create(ctx, v.ID, issue)
	if err != nil {
		return Issue{}, failed(span, "creating the issue", err)
	}
	span.SetAttributes(attribute.Int("cobranch.issue.number", created.Number))
	return s.rendered(ctx, created)
}

// assignee is the member of the organization id names, or nil for uuid.Nil; anyone else is a *orgs.ValidationError.
func (s *Service) assignee(ctx context.Context, orgID, id uuid.UUID) (*Principal, error) {
	if id == uuid.Nil {
		return nil, nil
	}
	p, err := s.store.Member(ctx, orgID, id)
	if errors.Is(err, ErrNotFound) {
		return nil, &orgs.ValidationError{Field: "assigneeId", Code: "assignee.invalid", Message: "not a member of this organization"}
	}
	if err != nil {
		return nil, fmt.Errorf("finding the assignee: %w", err)
	}
	return &p, nil
}

// repository is the organization's repository id names, which v reads, or nil for uuid.Nil; any other is a
// *orgs.ValidationError.
func (s *Service) repository(ctx context.Context, v Viewer, orgID, id uuid.UUID) (*Repository, error) {
	if id == uuid.Nil {
		return nil, nil
	}
	invalid := &orgs.ValidationError{Field: "repositoryId", Code: "repository.invalid", Message: "not a repository of this organization that you read"}
	r, err := s.store.Repository(ctx, orgID, id)
	if errors.Is(err, ErrNotFound) {
		return nil, invalid
	}
	if err != nil {
		return nil, fmt.Errorf("finding the repository: %w", err)
	}
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeRepository, id)
	if err != nil {
		return nil, fmt.Errorf("reading the viewer's permissions on the repository: %w", err)
	}
	if !slices.Contains(held, repos.PermRead) {
		return nil, invalid
	}
	return &r, nil
}

// failed records err on span unless it is an answer the domain gives, and wraps it with what failed.
func failed(span trace.Span, what string, err error) error {
	var verr *orgs.ValidationError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, orgs.ErrNotFound), errors.Is(err, ErrChanged), errors.As(err, &verr),
		errors.Is(err, auth.ErrForbidden):
		return err
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, what)
	return fmt.Errorf("%s: %w", what, err)
}

// wrap adds what failed to err, unless it is one of the domain's answers.
func wrap(what string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrChanged):
		return err
	}
	return fmt.Errorf("%s: %w", what, err)
}
