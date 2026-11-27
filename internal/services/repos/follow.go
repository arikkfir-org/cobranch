package repos

import (
	"context"
	"errors"
	"fmt"

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

// Shown is a repository as Get answers it: the repository, and what the viewer holds there.
type Shown struct {
	Repository Repository
	Held       []string
}

// FollowRepository sends the organization's repository slug names, with what v holds there, as Get answers them, at
// once, then again each time they change (it renamed or described anew, a grant that changes what v holds), until ctx
// ends or a read fails (the repository deleted, or v's access to it taken, among them), and then closes the channel.
// The repository's General settings follow it.
func (s *Service) FollowRepository(ctx context.Context, v Viewer, orgID uuid.UUID, slug string) (<-chan Shown, error) {
	shown, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) (Shown, error) {
		repo, held, err := s.Get(ctx, v, orgID, slug)
		return Shown{Repository: repo, Held: held}, gone(err)
	}, orgID)
	return shown, wrap("following the repository", err)
}

// FollowAccess sends who holds roles on the repository, as Access lists them, at once, then again each time they
// change (a role granted or revoked on the repository, or at the organization), until ctx ends or a read fails, and
// then closes the channel. The repository's Access settings follow it. orgID is the repository's organization, whose
// grants name it.
func (s *Service) FollowAccess(ctx context.Context, v Viewer, orgID, repoID uuid.UUID) (<-chan []Access, error) {
	list, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) ([]Access, error) {
		list, err := s.Access(ctx, v, repoID)
		return list, gone(err)
	}, orgID)
	return list, wrap("following the repository's access", err)
}

// gone marks ErrNotFound live.ErrGone, so that a follower of a repository deleted, or no longer v's to read, stops
// without reporting a failure.
func gone(err error) error {
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: %w", live.ErrGone, err)
	}
	return err
}
