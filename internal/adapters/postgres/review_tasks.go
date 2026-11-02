package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/repos"
)

// The AI reviewer's tasks: a review of each branch head a push brings, in the push worker's transaction, and another
// whenever a member asks (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/reviews.md, "The AI
// reviewer").

// errReviewed is a head the branch has a review task for already, open or ended with its review; errMovedOn a branch
// that a later push moved on from the head, which that push's review answers.
var (
	errReviewed = errors.New("the head has its review task")
	errMovedOn  = errors.New("the branch moved on from the head")
)

// outsideReason is why the review of a head pushed from outside the organization waits.
const outsideReason = "Pushed from outside the organization: a member starts its review."

// reviewRequest is a review queueReview queues: of the repository's branch at sha, which the pusher pushed, or of
// its head, whatever it is, for the member who asked for another review (by).
type reviewRequest struct {
	repoID uuid.UUID
	branch string
	sha    string
	pusher uuid.UUID
	by     uuid.UUID
}

// queueReviews is the push worker's step for the AI reviewer: it cancels the repository's open review tasks whose
// branch the push deleted or moved on from, then queues the review of the head of each branch but the default that the
// push created or moved, and still names. A reviewer that can't review, or settings that leave the branch out, queue
// nothing.
func queueReviews(ctx context.Context, q *queries.Queries, claimed queries.ClaimPushRow) error {
	if err := cancelStaleReviews(ctx, q, claimed.RepoID); err != nil {
		return err
	}
	var updates []refUpdateJSON
	if err := json.Unmarshal(claimed.RefUpdates, &updates); err != nil {
		return fmt.Errorf("reading the push's ref updates: %w", err)
	}
	r := reviewRequest{repoID: claimed.RepoID}
	if claimed.PusherID != nil {
		r.pusher = *claimed.PusherID
	}
	for _, u := range updates {
		name, isBranch := git.Branch(u.Name)
		if !isBranch || u.New == git.ZeroSHA || name == git.DefaultBranch {
			continue
		}
		r.branch, r.sha = name, u.New
		if _, err := queueReview(ctx, q, r); err != nil && !reviewsNothing(err) {
			return err
		}
	}
	return nil
}

// reviewsNothing reports whether err is why a push queues no review, rather than a failure.
func reviewsNothing(err error) bool {
	for _, skip := range []error{errReviewed, errMovedOn, repos.ErrNotFound, code.ErrNoReviewer, code.ErrReviewerNoModel,
		code.ErrReviewerSuspended, code.ErrReviewOff} {
		if errors.Is(err, skip) {
			return true
		}
	}
	return false
}

// cancelStaleReviews cancels the repository's open review tasks whose branch was deleted or moved on from their head,
// each recorded as its agent's own doing, as Cobranch's changes to its tasks are.
func cancelStaleReviews(ctx context.Context, q *queries.Queries, repoID uuid.UUID) error {
	rows, err := q.CancelStaleReviews(ctx, &repoID)
	if err != nil {
		return fmt.Errorf("cancelling the reviews of heads the branches moved on from: %w", err)
	}
	for _, t := range rows {
		if err := orgEvent(ctx, q, &t.OrgID, t.AgentID, "task.cancelled", t.ID, map[string]any{"number": t.Number}); err != nil {
			return err
		}
	}
	return nil
}

// queueReview queues the AI reviewer's review of r's head, assigned by the reviewer itself, so that it runs on its own
// grants. A review a member asks for supersedes the branch's open one and starts at once; one a push brings waits for
// a member when the push came from outside the organization, and none is queued for a head the branch has one for.
// It answers the review; code.ErrNoReviewer, code.ErrReviewerNoModel, code.ErrReviewerSuspended or code.ErrReviewOff
// when the reviewer reviews none of the branch, repos.ErrNotFound without the branch, errMovedOn or errReviewed.
func queueReview(ctx context.Context, q *queries.Queries, r reviewRequest) (code.AIReview, error) {
	reviewer, err := reviewerFor(ctx, q, r.repoID, r.branch)
	if err != nil {
		return code.AIReview{}, err
	}
	branch, err := q.ReviewedBranch(ctx, r.repoID, r.branch)
	if errors.Is(err, pgx.ErrNoRows) {
		return code.AIReview{}, repos.ErrNotFound
	}
	if err != nil {
		return code.AIReview{}, fmt.Errorf("reading branch %s: %w", r.branch, err)
	}
	if r.sha != "" && r.sha != branch.Head {
		return code.AIReview{}, errMovedOn
	}
	wait, err := makeRoom(ctx, q, r, branch)
	if err != nil {
		return code.AIReview{}, err
	}
	t := agents.Task{ID: uuid.Must(uuid.NewV7()), OrgID: reviewer.OrgID, Agent: agents.Principal{ID: reviewer.PrincipalID},
		AssignedBy: &agents.Principal{ID: reviewer.PrincipalID}, Kind: agents.KindReview, Title: code.ReviewTitle(r.branch, branch.Head),
		Instructions: code.ReviewInstructions(r.branch, branch.Head), RepoID: r.repoID, BranchID: branch.ID, SHA: branch.Head}
	actor := reviewer.PrincipalID
	if r.by != uuid.Nil {
		actor = r.by
	}
	return insertReview(ctx, q, actor, t, wait)
}

// reviewerFor finds the AI reviewer of the repository's organization, when it reviews the branch's pushes: it has a
// model and isn't suspended, and the default branch's /cobranch-review.yaml, or its defaults without one, takes the
// branch in.
func reviewerFor(ctx context.Context, q *queries.Queries, repoID uuid.UUID, branch string) (queries.ReviewerOfRow, error) {
	reviewer, err := q.ReviewerOf(ctx, repoID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return reviewer, code.ErrNoReviewer
	case err != nil:
		return reviewer, fmt.Errorf("finding the AI reviewer: %w", err)
	case !reviewer.HasModel:
		return reviewer, code.ErrReviewerNoModel
	case reviewer.Suspended:
		return reviewer, code.ErrReviewerSuspended
	}
	settings := git.DefaultReviewSettings()
	row, err := q.ReviewSettingsOf(ctx, repoID)
	switch {
	case err == nil:
		settings.Enabled, settings.Branches = row.Enabled, row.Branches
	case !errors.Is(err, pgx.ErrNoRows):
		return reviewer, fmt.Errorf("reading the AI review settings: %w", err)
	}
	if !settings.Enabled || !slices.ContainsFunc(settings.Branches, func(p string) bool { return jobs.Match(p, branch) }) {
		return reviewer, code.ErrReviewOff
	}
	return reviewer, nil
}

// makeRoom makes room for r's review of the branch's head: a member's request cancels the branch's open review
// tasks, in their name; a push's leaves a head that has its review alone (errReviewed), and reports whether its
// review waits for a member.
func makeRoom(ctx context.Context, q *queries.Queries, r reviewRequest, branch queries.ReviewedBranchRow) (bool, error) {
	if r.by == uuid.Nil {
		reviewed, err := q.HasReview(ctx, &branch.ID, &branch.Head)
		switch {
		case err != nil:
			return false, fmt.Errorf("looking for the review of branch %s at %s: %w", r.branch, branch.Head, err)
		case reviewed:
			return false, errReviewed
		}
		return waitsForMember(ctx, q, r.repoID, r.pusher)
	}
	rows, err := q.CancelBranchReviews(ctx, &r.by, "Another review was asked for.", &branch.ID)
	if err != nil {
		return false, fmt.Errorf("cancelling the open reviews of branch %s: %w", r.branch, err)
	}
	for _, t := range rows {
		if err := orgEvent(ctx, q, &t.OrgID, r.by, "task.cancelled", t.ID, map[string]any{"number": t.Number}); err != nil {
			return false, err
		}
	}
	return false, nil
}

// insertReview numbers the review task t in its organization and records it for actor, Queued, or Waiting for a
// member when wait is true, and answers it.
func insertReview(ctx context.Context, q *queries.Queries, actor uuid.UUID, t agents.Task, wait bool) (code.AIReview, error) {
	number, err := q.NextTaskNumber(ctx, t.OrgID)
	if err != nil {
		return code.AIReview{}, fmt.Errorf("numbering the review: %w", err)
	}
	if err := q.InsertTask(ctx, queries.InsertTaskParams{ID: t.ID, OrgID: t.OrgID, Number: number, AgentID: t.Agent.ID, Kind: t.Kind,
		AssignedBy: &t.AssignedBy.ID, Title: t.Title, Instructions: t.Instructions, RepoID: &t.RepoID, BranchID: &t.BranchID,
		Sha: &t.SHA}); err != nil {
		return code.AIReview{}, fmt.Errorf("recording the review: %w", err)
	}
	out := code.AIReview{Task: number, State: code.ReviewQueued}
	if wait {
		if err := q.HoldTask(ctx, outsideReason, t.ID); err != nil {
			return code.AIReview{}, fmt.Errorf("holding the review for a member: %w", err)
		}
		out.State, out.Reason = code.ReviewWaiting, outsideReason
	}
	details := map[string]any{"number": number, "agent": t.Agent.ID, "kind": t.Kind}
	if err := orgEvent(ctx, q, &t.OrgID, actor, "task.assigned", t.ID, details); err != nil {
		return code.AIReview{}, err
	}
	return out, nil
}

// RequestReview has the repository's organization's AI reviewer review the branch's head again for actor, under the
// repository's run lock, as the push worker queues reviews (code.Store).
func (s *CodeStore) RequestReview(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, branch string) (code.AIReview, error) {
	var out code.AIReview
	err := transact(ctx, s.pool, s.q, "requesting a review", actor, func(ctx context.Context, q *queries.Queries) error {
		if err := q.LockRunQueue(ctx, repoID); err != nil {
			return fmt.Errorf("locking the repository's runs: %w", err)
		}
		r, err := queueReview(ctx, q, reviewRequest{repoID: repoID, branch: branch, by: actor})
		out = r
		return err
	})
	return out, err
}
