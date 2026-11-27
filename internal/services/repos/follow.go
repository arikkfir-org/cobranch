package repos

import (
	"context"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/live"
)

// WithNotifier has the service's followers read an organization's repositories again as soon as n says something of
// the organization's changed; without it, they read them once a minute.
func (s *Service) WithNotifier(n live.Notifier) *Service {
	s.notifier = n
	return s
}

// FollowList sends the organization's repositories v reads, as List lists them, at once, then again each time they
// change (one created, renamed or deleted, a push, a branch that opens, merges or changes its status, main's checks),
// until ctx ends or a read fails, and then closes the channel. The Repositories page and the sidebar follow them.
func (s *Service) FollowList(ctx context.Context, v Viewer, orgID uuid.UUID) (<-chan []Listed, error) {
	list, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) ([]Listed, error) { return s.List(ctx, v, orgID) }, orgID)
	return list, wrap("following the repositories", err)
}

// FollowUsage sends what the organization takes of what its limits cap, as Usage answers it, at once, then again each
// time it changes, until ctx ends or a read fails, and then closes the channel. Settings · Usage follows it.
func (s *Service) FollowUsage(ctx context.Context, v Viewer, orgID uuid.UUID) (<-chan Usage, error) {
	usage, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) (Usage, error) { return s.Usage(ctx, v, orgID) }, orgID)
	return usage, wrap("following the organization's usage", err)
}
