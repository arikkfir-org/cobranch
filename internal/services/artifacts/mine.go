package artifacts

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/auth"
)

// person checks that v is a user, the one kind of principal that owns artifacts and grants on itself.
func person(v Viewer) error {
	if v.Kind != auth.KindUser {
		return fmt.Errorf("%w: only a person grants roles on their own artifacts", auth.ErrForbidden)
	}
	return nil
}

// signedIn checks that v is a user signed in with a session: a personal token, narrowed to some permissions, never
// widens who reaches its user's artifacts.
func signedIn(ctx context.Context, v Viewer) error {
	if err := person(v); err != nil {
		return err
	}
	if a, ok := auth.Viewer(ctx); !ok || a.User.ID != v.ID || a.Session.ID == uuid.Nil {
		return fmt.Errorf("%w: only a signed-in person, never a token, grants roles on their own artifacts", auth.ErrForbidden)
	}
	return nil
}

// MyAccess lists who lists or reads every artifact v owns: the roles principals hold on v, by display name. It takes
// a user.
func (s *Service) MyAccess(ctx context.Context, v Viewer) (_ []Grant, err error) {
	ctx, span := tracer.Start(ctx, "artifacts.Service.MyAccess", trace.WithAttributes(principalAttr(v.ID)))
	defer span.End()
	defer func() { record(span, err) }()
	if err := person(v); err != nil {
		return nil, err
	}
	grants, err := s.store.PersonGrants(ctx, v.ID)
	if err != nil {
		return nil, fmt.Errorf("listing who holds a role on the viewer: %w", err)
	}
	return grants, nil
}

// ShareMine gives a member of one of v's organizations, or an agent of one, role on v, lister or viewer, in place of
// any role it held there: it then lists, or reads, every artifact v owns in each organization both belong to. It takes
// a user signed in with a session.
func (s *Service) ShareMine(ctx context.Context, v Viewer, principalID uuid.UUID, role Role) (err error) {
	ctx, span := tracer.Start(ctx, "artifacts.Service.ShareMine", trace.WithAttributes(principalAttr(principalID)))
	defer span.End()
	defer func() { record(span, err) }()
	if err := signedIn(ctx, v); err != nil {
		return err
	}
	if role != RoleLister && role != RoleViewer {
		return invalid("role", "artifact.role_invalid", "a person grants lister or viewer on their artifacts")
	}
	if _, err := s.store.Colleague(ctx, v.ID, principalID); err != nil {
		return wrap("finding the principal", err)
	}
	return wrap("sharing the viewer's artifacts", s.store.SharePerson(ctx, v.ID, principalID, roleIDs[role]))
}

// UnshareMine takes every role a principal holds on v, or ErrNotFound when it holds none. It takes a user signed in
// with a session.
func (s *Service) UnshareMine(ctx context.Context, v Viewer, principalID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "artifacts.Service.UnshareMine", trace.WithAttributes(principalAttr(principalID)))
	defer span.End()
	defer func() { record(span, err) }()
	if err := signedIn(ctx, v); err != nil {
		return err
	}
	return wrap("unsharing the viewer's artifacts", s.store.UnsharePerson(ctx, v.ID, principalID))
}
