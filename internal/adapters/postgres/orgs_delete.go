package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/orgs"
)

// deletedReason is why the work of an organization being deleted ended.
const deletedReason = "its organization was deleted"

// Delete makes the organization Deleting at `at` for actor, if it is live and at version (any with orgs.AnyVersion),
// in one transaction that names actor: it frees its slugs, deletes its memberships, invitations and join requests, the
// grants there of whoever isn't its own and its API keys' secrets, and cancels its runs and tasks that haven't ended and
// its deliveries that haven't started applying (orgs.Store).
func (s *OrgStore) Delete(ctx context.Context, actor, orgID uuid.UUID, version int64, at time.Time) error {
	return s.transact(ctx, "deleting the organization", actor, func(ctx context.Context, q *queries.Queries) error {
		current, err := q.LockOrganizationVersion(ctx, orgID)
		if err != nil {
			return wrapOrg("reading the organization", err)
		}
		if version != orgs.AnyVersion && version != current {
			return orgs.ErrChanged
		}
		if err := q.MarkOrganizationDeleting(ctx, at, actor, orgID); err != nil {
			return fmt.Errorf("marking the organization deleting: %w", err)
		}
		if err := q.DeleteOrganizationAccess(ctx, orgID); err != nil {
			return fmt.Errorf("deleting the organization's slugs and members: %w", err)
		}
		if err := q.DeleteOrganizationGrants(ctx, orgID); err != nil {
			return fmt.Errorf("deleting the grants there: %w", err)
		}
		if err := q.DeleteOrganizationCredentials(ctx, orgID); err != nil {
			return fmt.Errorf("deleting the API keys' secrets: %w", err)
		}
		if err := q.CancelOrganizationWork(ctx, deletedReason, orgID, actor); err != nil {
			return fmt.Errorf("cancelling the organization's work: %w", err)
		}
		return nil
	})
}

// liveOrganization holds the organization of a repository against its deletion until the transaction ends and answers
// whether it is live (HoldRepositoryOrganization). What would start work there (a push the push worker follows, a run,
// a delivery other than a teardown) asks it first and queues nothing once the organization is being deleted, and a
// deletion that comes later waits for the hold, then cancels what was queued under it. A repository that is gone isn't
// live either.
func liveOrganization(ctx context.Context, q *queries.Queries, repoID uuid.UUID) (bool, error) {
	live, err := q.HoldRepositoryOrganization(ctx, repoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("holding repository %s's organization: %w", repoID, err)
	}
	return live, nil
}

// Purgeable lists the organizations being deleted whose rows may go, the earliest deleted first: those whose
// environments' teardowns all ended, and those deleted before `before` (orgs.Store).
func (s *OrgStore) Purgeable(ctx context.Context, before time.Time) ([]orgs.Purgeable, error) {
	rows, err := s.q.OrganizationsToPurge(ctx, before)
	if err != nil {
		return nil, fmt.Errorf("listing the organizations to purge: %w", err)
	}
	out := make([]orgs.Purgeable, 0, len(rows))
	for _, r := range rows {
		out = append(out, orgs.Purgeable{ID: r.ID, DeletingAt: r.DeletingAt, LiveEnvironments: r.LiveEnvironments})
	}
	return out, nil
}

// Purge deletes an organization being deleted, in a transaction that names nobody: its row, which takes every row that
// carries its org_id with it, and its events. False when it isn't one (orgs.Store).
func (s *OrgStore) Purge(ctx context.Context, orgID uuid.UUID) (bool, error) {
	var purged bool
	err := s.transact(ctx, "purging organization "+orgID.String(), uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		n, err := q.PurgeOrganization(ctx, orgID)
		if err != nil || n == 0 {
			return wrapIf(err, "deleting the organization's rows")
		}
		purged = true
		if err := q.DeleteOrganizationEvents(ctx, orgID); err != nil {
			return fmt.Errorf("deleting the organization's events: %w", err)
		}
		return nil
	})
	return purged, err
}
