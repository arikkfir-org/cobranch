package issues

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/orgs"
)

// Update changes the fields p names of one of the organization's issues, for v, and answers it as it now is; it takes
// issues.write, and repo.read on a repository it makes the issue about. A change that names a version the issue no
// longer is gets ErrChanged; one that changes nothing changes nothing.
func (s *Service) Update(ctx context.Context, v Viewer, orgID uuid.UUID, number int, p Patch) (Issue, error) {
	ctx, span := tracer.Start(ctx, "issues.Service.Update")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()), attribute.Int("cobranch.issue.number", number))
	if err := s.require(ctx, v, orgID, PermWrite); err != nil {
		return Issue{}, failed(span, "changing the issue", err)
	}
	if p.Version < 0 {
		return Issue{}, &orgs.ValidationError{Field: "version", Code: "version.invalid", Message: "a version of the issue, from 1"}
	}
	cur, err := s.store.Get(ctx, orgID, number)
	if err != nil {
		return Issue{}, failed(span, "changing the issue", err)
	}
	if Stale(p.Version, cur.Version) {
		return Issue{}, ErrChanged
	}
	next, changes, err := s.apply(ctx, v, cur, p)
	if err != nil {
		return Issue{}, failed(span, "changing the issue", err)
	}
	readable, err := s.readable(ctx, v, orgID)
	if err != nil {
		return Issue{}, failed(span, "changing the issue", err)
	}
	if len(changes) == 0 {
		return s.rendered(ctx, shown(cur, readable))
	}
	updated, err := s.store.Update(ctx, v.ID, next, cur.Version, changes)
	if err != nil {
		return Issue{}, failed(span, "changing the issue", err)
	}
	return s.rendered(ctx, shown(updated, readable))
}

// apply is cur with p's fields, validated, and the changes they make: the time it closed when its state closes it,
// none when it opens again.
func (s *Service) apply(ctx context.Context, v Viewer, cur Issue, p Patch) (Issue, []Change, error) {
	next, changes := cur, []Change(nil)
	if p.Title != nil {
		title, err := ValidateTitle(*p.Title)
		if err != nil {
			return Issue{}, nil, err
		}
		next.Title, changes = title, changed(changes, "title", cur.Title, title)
	}
	if p.Description != nil {
		if err := ValidateDescription(*p.Description); err != nil {
			return Issue{}, nil, err
		}
		if *p.Description != cur.Description {
			// The activity says the description changed, not what it said: it may be long.
			next.Description, changes = *p.Description, append(changes, Change{Field: "description"})
		}
	}
	if p.State != nil {
		if err := ValidateState("state", *p.State); err != nil {
			return Issue{}, nil, err
		}
		next.State, changes = *p.State, changed(changes, "state", string(cur.State), string(*p.State))
		next.ClosedAt = closedAt(cur, *p.State, s.now())
	}
	if p.Priority != nil {
		if err := ValidatePriority(*p.Priority); err != nil {
			return Issue{}, nil, err
		}
		next.Priority, changes = *p.Priority, changed(changes, "priority", cur.Priority.String(), p.Priority.String())
	}
	var err error
	if next, changes, err = s.reassign(ctx, cur, next, changes, p.AssigneeID); err != nil {
		return Issue{}, nil, err
	}
	return s.move(ctx, v, cur, next, changes, p.RepositoryID)
}

// reassign is next assigned to whom id names, and its change; next as it is for nil.
func (s *Service) reassign(ctx context.Context, cur, next Issue, changes []Change, id *uuid.UUID) (Issue, []Change, error) {
	if id == nil || *id == idOf(cur.Assignee) {
		return next, changes, nil
	}
	assignee, err := s.assignee(ctx, cur.OrgID, *id)
	if err != nil {
		return Issue{}, nil, err
	}
	next.Assignee = assignee
	c := Change{Field: "assignee", FromID: idOf(cur.Assignee), ToID: *id}
	if cur.Assignee != nil {
		c.From = cur.Assignee.DisplayName
	}
	if assignee != nil {
		c.To = assignee.DisplayName
	}
	return next, append(changes, c), nil
}

// move is next about the repository id names, and its change; next as it is for nil.
func (s *Service) move(ctx context.Context, v Viewer, cur, next Issue, changes []Change, id *uuid.UUID) (Issue, []Change, error) {
	from := uuid.Nil
	if cur.Repository != nil {
		from = cur.Repository.ID
	}
	if id == nil || *id == from {
		return next, changes, nil
	}
	repo, err := s.repository(ctx, v, cur.OrgID, *id)
	if err != nil {
		return Issue{}, nil, err
	}
	next.Repository = repo
	c := Change{Field: "repository", FromID: from, ToID: *id}
	if cur.Repository != nil {
		c.From = cur.Repository.Slug
	}
	if repo != nil {
		c.To = repo.Slug
	}
	return next, append(changes, c), nil
}

// changed appends field's change from from to to, unless they are the same.
func changed(changes []Change, field, from, to string) []Change {
	if from == to {
		return changes
	}
	return append(changes, Change{Field: field, From: from, To: to})
}

// closedAt is when an issue that was cur, now in state, closed: now when state closes it and cur was open, when cur
// closed while it stays closed, and nil while it is open.
func closedAt(cur Issue, state State, now time.Time) *time.Time {
	switch {
	case !state.Closed():
		return nil
	case cur.State.Closed():
		return cur.ClosedAt
	}
	return &now
}

func idOf(p *Principal) uuid.UUID {
	if p == nil {
		return uuid.Nil
	}
	return p.ID
}

// Delete deletes one of the organization's issues, with its activity; it takes issues.manage. A deletion that names a
// version the issue no longer is gets ErrChanged.
func (s *Service) Delete(ctx context.Context, v Viewer, orgID uuid.UUID, number int, version int64) error {
	ctx, span := tracer.Start(ctx, "issues.Service.Delete")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()), attribute.Int("cobranch.issue.number", number))
	if err := s.require(ctx, v, orgID, PermManage); err != nil {
		return failed(span, "deleting the issue", err)
	}
	if version < 0 {
		return &orgs.ValidationError{Field: "version", Code: "version.invalid", Message: "a version of the issue, from 1"}
	}
	issue, err := s.store.Get(ctx, orgID, number)
	if err != nil {
		return failed(span, "deleting the issue", err)
	}
	if Stale(version, issue.Version) {
		return ErrChanged
	}
	if err := s.store.Delete(ctx, v.ID, issue, issue.Version); err != nil {
		return failed(span, "deleting the issue", err)
	}
	return nil
}

// Activity lists what happened to one of the organization's issues, oldest first; it takes issues.read. A change of
// its repository names only the repositories v reads.
func (s *Service) Activity(ctx context.Context, v Viewer, orgID uuid.UUID, number int) ([]Event, error) {
	ctx, span := tracer.Start(ctx, "issues.Service.Activity")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()), attribute.Int("cobranch.issue.number", number))
	issue, err := s.get(ctx, v, orgID, number, PermRead)
	if err != nil {
		return nil, failed(span, "reading the issue's activity", err)
	}
	events, err := s.store.Activity(ctx, issue.ID)
	if err != nil {
		return nil, failed(span, "reading the issue's activity", err)
	}
	readable, err := s.readable(ctx, v, orgID)
	if err != nil {
		return nil, failed(span, "reading the issue's activity", err)
	}
	return hideRepositories(events, readable), nil
}

// hideRepositories blanks the slugs of the repositories outside readable in events' changes.
func hideRepositories(events []Event, readable []uuid.UUID) []Event {
	for i, e := range events {
		for j, c := range e.Changes {
			if c.Field != "repository" {
				continue
			}
			if c.FromID != uuid.Nil && !slices.Contains(readable, c.FromID) {
				events[i].Changes[j].From = ""
			}
			if c.ToID != uuid.Nil && !slices.Contains(readable, c.ToID) {
				events[i].Changes[j].To = ""
			}
		}
	}
	return events
}
