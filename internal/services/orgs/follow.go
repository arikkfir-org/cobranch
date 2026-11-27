package orgs

import (
	"context"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/live"
)

// WithChanges has the service's followers read an organization's members, invitations, join requests, roles and API
// keys again as soon as n says something of the organization's changed; without it, they read them once a minute.
func (s *Service) WithChanges(n live.Notifier) *Service {
	s.changes = n
	return s
}

// FollowMembers sends the organization's members, as Members lists them, at once, then again each time they change,
// until ctx ends or a read fails, and then closes the channel. Settings · Members follows them this way.
func (s *Service) FollowMembers(ctx context.Context, v Viewer, orgID uuid.UUID) (<-chan []Member, error) {
	return followOrg(ctx, s, "orgs.Service.FollowMembers", orgID, func(ctx context.Context) ([]Member, error) { return s.Members(ctx, v, orgID) })
}

// FollowInvitations sends the organization's pending invitations, as Invitations lists them, at once, then again each
// time they change, until ctx ends or a read fails, and then closes the channel. Settings · Members follows them.
func (s *Service) FollowInvitations(ctx context.Context, v Viewer, orgID uuid.UUID) (<-chan []Invitation, error) {
	return followOrg(ctx, s, "orgs.Service.FollowInvitations", orgID, func(ctx context.Context) ([]Invitation, error) { return s.Invitations(ctx, v, orgID) })
}

// FollowJoinRequests sends the organization's pending requests to join, as JoinRequests lists them, at once, then
// again each time they change, until ctx ends or a read fails, and then closes the channel. Settings · Members follows
// them.
func (s *Service) FollowJoinRequests(ctx context.Context, v Viewer, orgID uuid.UUID) (<-chan []JoinRequest, error) {
	return followOrg(ctx, s, "orgs.Service.FollowJoinRequests", orgID, func(ctx context.Context) ([]JoinRequest, error) { return s.JoinRequests(ctx, v, orgID) })
}

// FollowRoles sends the roles the organization grants, as Roles lists them, at once, then again each time they or
// their holders change, until ctx ends or a read fails, and then closes the channel. Settings · Roles follows them.
func (s *Service) FollowRoles(ctx context.Context, v Viewer, orgID uuid.UUID) (<-chan []Role, error) {
	return followOrg(ctx, s, "orgs.Service.FollowRoles", orgID, func(ctx context.Context) ([]Role, error) { return s.Roles(ctx, v, orgID) })
}

// FollowAPIKeys sends the organization's API keys, as APIKeys lists them, at once, then again each time they change,
// until ctx ends or a read fails, and then closes the channel. Settings · API keys follows them.
func (s *Service) FollowAPIKeys(ctx context.Context, v Viewer, orgID uuid.UUID) (<-chan []APIKey, error) {
	return followOrg(ctx, s, "orgs.Service.FollowAPIKeys", orgID, func(ctx context.Context) ([]APIKey, error) { return s.APIKeys(ctx, v, orgID) })
}

// followOrg follows what read lists of the organization, under a span of name, reading again whenever the service's
// changes say something of the organization changed.
func followOrg[T any](ctx context.Context, s *Service, name string, orgID uuid.UUID, read func(context.Context) ([]T, error)) (_ <-chan []T, err error) {
	ctx, span := tracer.Start(ctx, name, trace.WithAttributes(attribute.String("cobranch.organization.id", orgID.String())))
	defer span.End()
	defer func() { record(span, err) }()
	lists, err := live.Follow(ctx, s.changes, live.Poll, read, orgID)
	return lists, wrap("following the organization's list", err)
}
