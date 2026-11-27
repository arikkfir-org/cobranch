package issues

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/live"
	"cobranch.dev/internal/services/orgs"
)

// WithNotifier has the service's followers read an organization's issues again as soon as n says something of the
// organization's changed; without it, they read them once a minute.
func (s *Service) WithNotifier(n live.Notifier) *Service {
	s.notifier = n
	return s
}

// FollowList sends the page of the organization's issues that f names, as List answers it, at once, then again each
// time it changes (an issue created, changed, closed, assigned or deleted, a label renamed, the task or branch an issue
// shows moving on), until ctx ends or a read fails, and then closes the channel. The issue lists and the sidebar
// follow it.
func (s *Service) FollowList(ctx context.Context, v Viewer, orgID uuid.UUID, f Filter) (<-chan Page, error) {
	pages, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) (Page, error) {
		page, err := s.List(ctx, v, orgID, f)
		return page, gone(err)
	}, orgID)
	return pages, wrap("following the issues", err)
}

// FollowIssue sends one of the organization's issues, as Get answers it, at once, then again each time it changes,
// until ctx ends or a read fails (the issue deleted among them), and then closes the channel. The issue's page follows
// it.
func (s *Service) FollowIssue(ctx context.Context, v Viewer, orgID uuid.UUID, number int) (<-chan Issue, error) {
	issue, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) (Issue, error) {
		issue, err := s.Get(ctx, v, orgID, number)
		return issue, gone(err)
	}, orgID)
	return issue, wrap("following the issue", err)
}

// FollowActivity sends what happened to one of the organization's issues, as Activity lists it, at once, then again
// each time something more does (a change, a branch linked, a task's progress), until ctx ends or a read fails, and
// then closes the channel. The issue's page follows it.
func (s *Service) FollowActivity(ctx context.Context, v Viewer, orgID uuid.UUID, number int) (<-chan []Event, error) {
	events, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) ([]Event, error) {
		events, err := s.Activity(ctx, v, orgID, number)
		return events, gone(err)
	}, orgID)
	return events, wrap("following the issue's activity", err)
}

// FollowBranches sends the branches linked to one of the organization's issues, as Branches lists them, at once, then
// again each time they change (one linked or unlinked, pushed to, merged or deleted), until ctx ends or a read fails,
// and then closes the channel. The issue's page follows them.
func (s *Service) FollowBranches(ctx context.Context, v Viewer, orgID uuid.UUID, number int) (<-chan []LinkedBranch, error) {
	branches, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) ([]LinkedBranch, error) {
		branches, err := s.Branches(ctx, v, orgID, number)
		return branches, gone(err)
	}, orgID)
	return branches, wrap("following the issue's branches", err)
}

// FollowBranchLink sends the issue a repository's branch works on, as OfBranch answers it, at once, then again each
// time it changes (the branch linked to an issue, or unlinked), until ctx ends or a read fails, and then closes the
// channel. It follows the repository, which a link names (migration 00076): a branch's page follows it beside the
// branch, and the issue's own title and state show at the page's next ask.
func (s *Service) FollowBranchLink(ctx context.Context, v Viewer, orgID, repoID uuid.UUID, name string) (<-chan *Link, error) {
	link, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) (*Link, error) {
		link, err := s.OfBranch(ctx, v, orgID, repoID, name)
		return link, gone(err)
	}, repoID)
	return link, wrap("following the branch's issue", err)
}

// gone marks err live.ErrGone when it says that what a follower reads is gone or no longer the viewer's to read (the
// issue deleted, the organization or the viewer's access to it), so that the follower stops without reporting a
// failure.
func gone(err error) error {
	if errors.Is(err, ErrNotFound) || errors.Is(err, orgs.ErrNotFound) || errors.Is(err, auth.ErrForbidden) {
		return fmt.Errorf("%w: %w", live.ErrGone, err)
	}
	return err
}
