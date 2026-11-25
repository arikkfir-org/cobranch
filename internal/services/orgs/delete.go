package orgs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/auth"
)

var tracer = otel.Tracer("cobranch.dev/internal/services/orgs")

// AnyVersion deletes or updates whichever version of an organization is there (If-Match: *).
const AnyVersion int64 = -1

// PurgeWait is the longest a deleted organization's rows wait for its environments' teardowns: a cluster that never
// answers doesn't keep them.
const PurgeWait = 24 * time.Hour

// Purgeable is an organization being deleted whose rows may go: when it was deleted, and how many of its environments
// live still, which a teardown that failed, or one that didn't end within PurgeWait, leaves in their clusters.
type Purgeable struct {
	ID               uuid.UUID
	DeletingAt       time.Time
	LiveEnvironments int64
}

// Delete deletes the organization for v, who holds org.manage there, if it is at version (any with AnyVersion): it is
// Deleting at once, gone for everyone, its slugs free, its members' access and its API keys' secrets gone and its work
// cancelled, and maintenance purges its rows once its environments are torn down (Purge). The audit log names v on
// its row. ErrChanged when it changed since version, ErrNotFound when it is gone or v may not see it.
func (s *Service) Delete(ctx context.Context, v Viewer, orgID uuid.UUID, version int64) (err error) {
	ctx, span := tracer.Start(ctx, "orgs.Service.Delete", trace.WithAttributes(attribute.String("cobranch.organization.id", orgID.String())))
	defer span.End()
	defer func() { record(span, err) }()
	if _, _, err := s.require(ctx, v, orgID, PermManage); err != nil {
		return err
	}
	return wrap("deleting the organization", s.store.Delete(ctx, v.ID, orgID, version, s.now()))
}

// Purge deletes the rows of the organizations being deleted whose environments' teardowns all ended, or that were
// deleted PurgeWait ago or longer, each with every row that carries its org_id and its events, and logs each one that
// leaves environments in their clusters. It goes on past an organization it fails to purge, and answers how many it
// purged. Maintenance runs it, as Cobranch itself: the purge's deletes name nobody.
func (s *Service) Purge(ctx context.Context) (_ int, err error) {
	ctx, span := tracer.Start(ctx, "orgs.Service.Purge")
	defer span.End()
	defer func() { record(span, err) }()
	due, err := s.store.Purgeable(ctx, s.now().Add(-PurgeWait))
	if err != nil {
		return 0, fmt.Errorf("listing the organizations to purge: %w", err)
	}
	purged := 0
	var errs []error
	for _, org := range due {
		ok, err := s.store.Purge(ctx, org.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("purging organization %s: %w", org.ID, err))
			continue
		}
		if !ok {
			continue
		}
		purged++
		if org.LiveEnvironments > 0 {
			slog.WarnContext(ctx, "Purged a deleted organization whose environments weren't all torn down: what they hold stays in their clusters",
				"org", org.ID, "deletingAt", org.DeletingAt, "environments", org.LiveEnvironments)
		}
	}
	span.SetAttributes(attribute.Int("cobranch.organizations.purged", purged))
	return purged, errors.Join(errs...)
}

// record marks the span failed with err, unless it is one the domain answers.
func record(span trace.Span, err error) {
	switch {
	case err == nil, errors.Is(err, ErrNotFound), errors.Is(err, ErrChanged), errors.Is(err, auth.ErrForbidden):
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
