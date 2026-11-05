package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
)

// activityKinds are the kinds of activity, by the kind of the event each is.
var activityKinds = map[string]code.ActivityKind{
	"repo.pushed":      code.ActivityPushed,
	"repo.created":     code.ActivityCreated,
	"review.submitted": code.ActivityReviewed,
	"delivery.ended":   code.ActivityDelivered,
}

// Activity lists, newest first, at most limit of what happened in the organization's repositories of repoIDs from
// before (the zero cursor for the newest), read from the organization's events and its deliveries' ends (code.Store).
func (s *CodeStore) Activity(ctx context.Context, orgID uuid.UUID, repoIDs []uuid.UUID, before code.ActivityCursor, limit int) ([]code.Activity, error) {
	params := queries.OrganizationActivityParams{OrgID: &orgID, RepoIds: repoIDs, BeforeID: before.ID, Lim: int32(min(limit, 1000))} //nolint:gosec // at most 1000
	if before != (code.ActivityCursor{}) {
		params.BeforeAt = &before.At
	}
	rows, err := s.q.OrganizationActivity(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("listing the activity of organization %s: %w", orgID, err)
	}
	out := make([]code.Activity, 0, len(rows))
	for _, r := range rows {
		a, err := activityOf(r)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// activityOf is the domain's view of an event or a delivery's end OrganizationActivity read: a push that brought
// branches into the default branch is a merge.
func activityOf(r queries.OrganizationActivityRow) (code.Activity, error) {
	a := code.Activity{ID: r.ID, At: r.At, Kind: activityKinds[r.Kind], Actor: code.Actor{ID: r.ActorID, DisplayName: r.ActorName, Kind: r.ActorKind},
		RepoID: r.RepoID, Repo: r.RepoSlug}
	switch a.Kind {
	case code.ActivityPushed:
		var refs []refUpdateJSON
		if err := json.Unmarshal(r.RefUpdates, &refs); err != nil {
			return code.Activity{}, fmt.Errorf("reading the refs of the push of event %s: %w", r.ID, err)
		}
		var merged []headJSON
		if err := json.Unmarshal(r.Merged, &merged); err != nil {
			return code.Activity{}, fmt.Errorf("reading the merges of the push of event %s: %w", r.ID, err)
		}
		for _, u := range refs {
			a.Refs = append(a.Refs, git.RefUpdate(u))
		}
		for _, h := range merged {
			a.Merged = append(a.Merged, git.Head{Branch: h.Branch, SHA: h.SHA})
		}
		if len(a.Merged) > 0 {
			a.Kind = code.ActivityMerged
		}
		a.Commits = int(r.Commits)
	case code.ActivityReviewed:
		a.SHA, a.Review, a.Branch = r.Sha, code.ReviewState(r.ReviewState), r.ReviewBranch
	case code.ActivityDelivered:
		a.SHA = r.Sha
		a.Delivery = &code.ActivityDelivery{Number: r.DeliveryNumber, Kind: code.DeliveryKind(r.DeliveryKind), Environment: r.Environment,
			State: code.DeliveryState(r.DeliveryState)}
	}
	return a, nil
}
