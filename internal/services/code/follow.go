package code

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/live"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// WithNotifier has the service's followers read what they follow again as soon as n says something of its
// organization's, or of its repository's, changed; without it, they read it once a minute.
func (s *Service) WithNotifier(n live.Notifier) *Service {
	s.notifier = n
	return s
}

// FollowAttention sends the branches of the organization that need v, as Attention lists them, at once, then again
// each time they change, until ctx ends or a read fails, and then closes the channel. The Activity page follows them.
func (s *Service) FollowAttention(ctx context.Context, v Viewer, orgID uuid.UUID) (<-chan []Attention, error) {
	ctx, span := tracer.Start(ctx, "code.Service.FollowAttention")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()))
	list, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) ([]Attention, error) { return s.Attention(ctx, v, orgID) }, orgID)
	return list, failed(span, "following the branches that need the viewer", err)
}

// FollowActivity sends the page of what happened in the organization that Activity lists before the cursor at once,
// then again each time it changes, until ctx ends or a read fails, and then closes the channel. The Activity page
// follows the newest page.
func (s *Service) FollowActivity(ctx context.Context, v Viewer, orgID uuid.UUID, before ActivityCursor, limit int) (<-chan ActivityPage, error) {
	ctx, span := tracer.Start(ctx, "code.Service.FollowActivity")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()))
	page, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) (ActivityPage, error) {
		return s.Activity(ctx, v, orgID, before, limit)
	}, orgID)
	return page, failed(span, "following the activity", err)
}

// FollowBranches sends the repository's live branches, as Branches lists them, at once, then again each time they
// change (a push, a branch opened, merged or deleted, a check or a review that moves a status), until ctx ends or a
// read fails, and then closes the channel. The Branches tab follows them.
func (s *Service) FollowBranches(ctx context.Context, v Viewer, repoID uuid.UUID) (<-chan []Branch, error) {
	ctx, span := tracer.Start(ctx, "code.Service.FollowBranches")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()))
	list, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) ([]Branch, error) {
		branches, err := s.Branches(ctx, v, repoID)
		return branches, gone(err)
	}, repoID)
	return list, failed(span, "following the branches", err)
}

// FollowBranch sends one of the repository's live branches, as Branch answers it, at once, then again each time it
// changes (a push, its checks, reviews and status, its merge), until ctx ends or a read fails (the branch deleted among
// them), and then closes the channel. A branch's page follows it, and the Files page the branch it shows.
func (s *Service) FollowBranch(ctx context.Context, v Viewer, repoID uuid.UUID, name string) (<-chan Branch, error) {
	ctx, span := tracer.Start(ctx, "code.Service.FollowBranch")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", name))
	b, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) (Branch, error) {
		b, err := s.Branch(ctx, v, repoID, name)
		return b, gone(err)
	}, repoID)
	return b, failed(span, "following the branch", err)
}

// FollowHistory sends the page of history q asks for, as History answers it, at once, then again each time it changes
// (a push to the branch), until ctx ends or a read fails, and then closes the channel. The Commits page and a branch's
// commits follow their first page.
func (s *Service) FollowHistory(ctx context.Context, v Viewer, repoID uuid.UUID, q HistoryQuery) (<-chan Page, error) {
	ctx, span := tracer.Start(ctx, "code.Service.FollowHistory")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", q.Branch))
	page, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) (Page, error) {
		page, err := s.History(ctx, v, repoID, q)
		return page, gone(err)
	}, repoID)
	return page, failed(span, "following the history", err)
}

// FollowRuns sends the page of the repository's runs q asks for, as Runs lists them, at once, then again each time it
// changes (a run queued, started, ended or cancelled), until ctx ends or a read fails, and then closes the channel. The
// Jobs tab and a branch's Jobs tab follow their first page.
func (s *Service) FollowRuns(ctx context.Context, v Viewer, repoID uuid.UUID, q RunsQuery) (<-chan RunPage, error) {
	ctx, span := tracer.Start(ctx, "code.Service.FollowRuns")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()))
	page, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) (RunPage, error) {
		page, err := s.Runs(ctx, v, repoID, q)
		return page, gone(err)
	}, repoID)
	return page, failed(span, "following the runs", err)
}

// FollowAnnotations sends the annotations on one of the repository's commits, as Annotations lists them, at once, then
// again each time they change (one set, changed or deleted), until ctx ends or a read fails, and then closes the
// channel. A branch's page follows those on its head.
func (s *Service) FollowAnnotations(ctx context.Context, v Viewer, repoID uuid.UUID, sha string) (<-chan []Annotation, error) {
	ctx, span := tracer.Start(ctx, "code.Service.FollowAnnotations")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.commit.sha", sha))
	list, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) ([]Annotation, error) {
		list, err := s.Annotations(ctx, v, repoID, sha)
		return list, gone(err)
	}, repoID)
	return list, failed(span, "following the annotations", err)
}

// FollowReviews sends the page of the reviews on one of the repository's commits that q asks for, as Reviews lists
// them, at once, then again each time it changes (a review submitted), until ctx ends or a read fails, and then closes
// the channel. A branch's page follows those of its head.
func (s *Service) FollowReviews(ctx context.Context, v Viewer, repoID uuid.UUID, sha string, q ReviewQuery) (<-chan ReviewPage, error) {
	ctx, span := tracer.Start(ctx, "code.Service.FollowReviews")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.commit.sha", sha))
	page, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) (ReviewPage, error) {
		page, err := s.Reviews(ctx, v, repoID, sha, q)
		return page, gone(err)
	}, repoID)
	return page, failed(span, "following the reviews", err)
}

// FollowEnvironments sends the repository's live environments, those of branch alone when it is given, as
// Environments lists them, at once, then again each time they change (one made or torn down, a delivery that moves,
// its health, a pause), until ctx ends or a read fails, and then closes the channel. The Delivery page and a branch's
// Environment tab follow them.
func (s *Service) FollowEnvironments(ctx context.Context, v Viewer, repoID uuid.UUID, branch string) (<-chan []Environment, error) {
	ctx, span := tracer.Start(ctx, "code.Service.FollowEnvironments")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", branch))
	list, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) ([]Environment, error) {
		list, err := s.Environments(ctx, v, repoID, branch)
		return list, gone(err)
	}, repoID)
	return list, failed(span, "following the environments", err)
}

// FollowEnvironment sends one of the repository's live environments, as Environment answers it, at once, then again
// each time it changes, until ctx ends or a read fails (the environment torn down among them), and then closes the
// channel. An environment's page follows it.
func (s *Service) FollowEnvironment(ctx context.Context, v Viewer, repoID, id uuid.UUID) (<-chan Environment, error) {
	ctx, span := tracer.Start(ctx, "code.Service.FollowEnvironment")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.environment.id", id.String()))
	e, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) (Environment, error) {
		e, err := s.Environment(ctx, v, repoID, id)
		return e, gone(err)
	}, repoID)
	return e, failed(span, "following the environment", err)
}

// FollowEnvironmentObjects sends what one of the repository's environments holds, as EnvironmentObjects lists it, at
// once, then again each time it changes (an apply, an object's health), until ctx ends or a read fails, and then
// closes the channel. An environment's card follows them.
func (s *Service) FollowEnvironmentObjects(ctx context.Context, v Viewer, repoID, environmentID uuid.UUID) (<-chan []EnvironmentObject, error) {
	ctx, span := tracer.Start(ctx, "code.Service.FollowEnvironmentObjects")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.environment.id", environmentID.String()))
	list, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) ([]EnvironmentObject, error) {
		list, err := s.EnvironmentObjects(ctx, v, repoID, environmentID)
		return list, gone(err)
	}, repoID)
	return list, failed(span, "following the environment's objects", err)
}

// FollowDeliveryTargets sends the targets the repository's default branch declares, as DeliveryTargets lists them, at
// once, then again each time they change (a push to it), until ctx ends or a read fails, and then closes the channel.
// The Delivery page follows them.
func (s *Service) FollowDeliveryTargets(ctx context.Context, v Viewer, repoID uuid.UUID) (<-chan []DeliveryTarget, error) {
	ctx, span := tracer.Start(ctx, "code.Service.FollowDeliveryTargets")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()))
	list, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) ([]DeliveryTarget, error) {
		list, err := s.DeliveryTargets(ctx, v, repoID)
		return list, gone(err)
	}, repoID)
	return list, failed(span, "following the targets", err)
}

// FollowDeliveries sends the page of one of the repository's environments' deliveries that Deliveries lists for kinds,
// before and limit, at once, then again each time it changes (one queued, started or ended), until ctx ends or a read
// fails, and then closes the channel. An environment's card follows its first page.
func (s *Service) FollowDeliveries(ctx context.Context, v Viewer, repoID, environmentID uuid.UUID, kinds []DeliveryKind, before int64,
	limit int) (<-chan DeliveryPage, error) {
	ctx, span := tracer.Start(ctx, "code.Service.FollowDeliveries")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.environment.id", environmentID.String()))
	page, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) (DeliveryPage, error) {
		page, err := s.Deliveries(ctx, v, repoID, environmentID, kinds, before, limit)
		return page, gone(err)
	}, repoID)
	return page, failed(span, "following the deliveries", err)
}

// gone marks err live.ErrGone when it says that what a follower reads is gone or no longer the viewer's to read (a
// branch deleted, its ref with it, the repository or the viewer's access to it), so that the follower stops without
// reporting a failure.
func gone(err error) error {
	if errors.Is(err, repos.ErrNotFound) || errors.Is(err, ErrRefNotFound) || errors.Is(err, orgs.ErrNotFound) || errors.Is(err, auth.ErrForbidden) {
		return fmt.Errorf("%w: %w", live.ErrGone, err)
	}
	return err
}
