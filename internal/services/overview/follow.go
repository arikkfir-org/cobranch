package overview

import (
	"context"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/live"
)

// WithNotifier has the service's followers read an organization's Overview again as soon as n says something of the
// organization's changed; without it, they read it once a minute.
func (s *Service) WithNotifier(n live.Notifier) *Service {
	s.notifier = n
	return s
}

// FollowOverview sends the organization's Overview over p, as Overview answers it, at once, then again each time it
// changes, until ctx ends or a read fails, and then closes the channel. The Overview page follows it.
func (s *Service) FollowOverview(ctx context.Context, v Viewer, orgID uuid.UUID, p Period) (<-chan Overview, error) {
	ctx, span := tracer.Start(ctx, "overview.Service.FollowOverview", trace.WithAttributes(attribute.String("cobranch.organization.id", orgID.String())))
	defer span.End()
	o, err := live.Follow(ctx, s.notifier, live.Poll, func(ctx context.Context) (Overview, error) { return s.Overview(ctx, v, orgID, p) }, orgID)
	if err != nil {
		return nil, failed(span, "following the overview", err)
	}
	return o, nil
}
