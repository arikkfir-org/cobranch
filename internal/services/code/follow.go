package code

import (
	"context"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/live"
)

// WithNotifier has the service's followers read an organization's branches that need a viewer and its activity again
// as soon as n says something of the organization's changed; without it, they read them once a minute.
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
