package artifacts

import (
	"context"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/live"
)

// WithNotifier has the service's followers read an organization's artifacts again as soon as n says they may have
// changed; without it, they read them once a minute.
func (s *Service) WithNotifier(n live.Notifier) *Service {
	s.notifier = n
	return s
}

// FollowOrganization sends the organization's artifacts that ListOrganization lists for v and f at once, then again
// each time they change (one made, revised, shared or deleted), until ctx ends or a read fails, and then closes the
// channel. The Artifacts page and the sidebar follow them.
func (s *Service) FollowOrganization(ctx context.Context, v Viewer, orgID uuid.UUID, f Filter) (_ <-chan []Artifact, err error) {
	ctx, span := tracer.Start(ctx, "artifacts.Service.FollowOrganization", trace.WithAttributes(orgAttr(orgID)))
	defer span.End()
	defer func() { record(span, err) }()
	list, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) ([]Artifact, error) {
		return s.ListOrganization(ctx, v, orgID, f)
	}, orgID)
	return list, wrap("following the artifacts", err)
}
