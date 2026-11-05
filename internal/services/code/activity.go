package code

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// ActivityKind is what happened in a repository, as an organization's activity lists it.
type ActivityKind string

// The kinds of activity.
const (
	// ActivityPushed is a push that merged nothing into the default branch: the branches and tags it created, moved
	// and deleted.
	ActivityPushed ActivityKind = "pushed"
	// ActivityMerged is a push that brought branches' heads into the default branch: a merge pushed, or one Cobranch
	// made when asked.
	ActivityMerged ActivityKind = "merged"
	// ActivityReviewed is a review submitted of a commit.
	ActivityReviewed ActivityKind = "reviewed"
	// ActivityDelivered is a delivery to one of the repository's environments that ended, succeeded or failed: a
	// commit delivered or an environment torn down, by the environment's target. Applying the last render again, as
	// the controller does every few minutes, is left out.
	ActivityDelivered ActivityKind = "delivered"
	// ActivityCreated is the repository's creation.
	ActivityCreated ActivityKind = "created"
)

// Actor is who did something: a principal by its ID, its display name and its kind (user, agent, job, api_key, ...);
// a principal deleted since keeps its ID, with no name or kind.
type Actor struct {
	ID          uuid.UUID
	DisplayName string
	Kind        string
}

// ActivityDelivery is the delivery an ActivityDelivered ended: its number, what it did, the branch of the environment
// it delivered to, and how it ended.
type ActivityDelivery struct {
	Number      int64
	Kind        DeliveryKind
	Environment string
	State       DeliveryState
}

// Activity is one thing that happened in a repository of an organization, by the ID of the event or delivery it is:
// who did it, when, in which repository by its ID and slug, and what the kind names. A push names the refs it moved
// (Refs) and the commits it brought (Commits), and a merge the branches whose heads it brought into the default branch
// (Merged); a review names its commit (SHA), its verdict (Review) and the live branch whose head it is, if any
// (Branch); a delivery names its commit (SHA, none for a teardown) and itself (Delivery).
type Activity struct {
	ID       uuid.UUID
	At       time.Time
	Kind     ActivityKind
	Actor    Actor
	RepoID   uuid.UUID
	Repo     string
	Refs     []git.RefUpdate
	Commits  int
	Merged   []git.Head
	SHA      string
	Review   ReviewState
	Branch   string
	Delivery *ActivityDelivery
}

// ActivityCursor is a point in an organization's activity, which a page starts before: an activity's time and ID, which
// orders those of the same time. The zero cursor is after the newest.
type ActivityCursor struct {
	At time.Time
	ID uuid.UUID
}

// ActivityPage is a page of an organization's activity, newest first, and where its next page starts; the zero cursor
// after the last.
type ActivityPage struct {
	Activity []Activity
	Next     ActivityCursor
}

// MaxActivityPage is the most activity a page holds.
const MaxActivityPage = 100

// Activity lists a page of what happened in the organization's repositories that v reads, newest first, from before
// (the zero cursor for the newest): pushes, merges, reviews, deliveries that ended and repositories created.
// It takes org.read, and answers orgs.ErrNotFound without it; limit 0 asks for DefaultPageSize. The organization's
// Activity page shows it.
func (s *Service) Activity(ctx context.Context, v Viewer, orgID uuid.UUID, before ActivityCursor, limit int) (ActivityPage, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Activity")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()))
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeOrganization, orgID)
	if err != nil {
		return ActivityPage{}, failed(span, "listing the activity", fmt.Errorf("reading the viewer's permissions: %w", err))
	}
	if !slices.Contains(held, orgs.PermRead) {
		return ActivityPage{}, failed(span, "listing the activity", orgs.ErrNotFound)
	}
	if limit < 0 || limit > MaxActivityPage {
		return ActivityPage{}, &orgs.ValidationError{Field: "limit", Code: "limit.invalid", Message: fmt.Sprintf("at most %d events a page", MaxActivityPage)}
	}
	if limit == 0 {
		limit = DefaultPageSize
	}
	readable, err := s.authz.Repositories(ctx, v.ID, orgID, repos.PermRead)
	if err != nil {
		return ActivityPage{}, failed(span, "listing the activity", fmt.Errorf("listing the repositories the viewer reads: %w", err))
	}
	if len(readable) == 0 {
		return ActivityPage{}, nil
	}
	list, err := s.store.Activity(ctx, orgID, readable, before, limit+1)
	if err != nil {
		return ActivityPage{}, failed(span, "listing the activity", err)
	}
	page := ActivityPage{Activity: list}
	if len(list) > limit {
		page.Activity = list[:limit]
		page.Next = ActivityCursor{At: list[limit-1].At, ID: list[limit-1].ID}
	}
	return page, nil
}
