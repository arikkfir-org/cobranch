package code

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

// The AI reviewer's reviews of branch heads
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/reviews.md, "The AI reviewer").
var (
	// ErrNoReviewer is a request for a review in an organization that has no AI reviewer yet.
	ErrNoReviewer = errors.New("the organization has no AI reviewer yet")
	// ErrReviewerNoModel is a request for a review while the AI reviewer has no model.
	ErrReviewerNoModel = errors.New("the AI reviewer has no model: an organization admin picks its provider and model")
	// ErrReviewerSuspended is a request for a review while the AI reviewer is suspended.
	ErrReviewerSuspended = errors.New("the AI reviewer is suspended")
	// ErrReviewOff is a request for a review of a branch whose pushes the AI reviewer doesn't review: the default
	// branch, or one that the default branch's /cobranch-review.yaml leaves out.
	ErrReviewOff = errors.New("the AI reviewer reviews none of this branch's pushes, as main's /cobranch-review.yaml says")
)

// ReviewTaskState is where the AI reviewer's task for a branch head stands: an agent task's state.
type ReviewTaskState string

// The states of a review task.
const (
	// ReviewWaiting waits for a member to start it: its head was pushed from outside the organization.
	ReviewWaiting ReviewTaskState = "waiting"
	// ReviewQueued waits for the controller to run it.
	ReviewQueued ReviewTaskState = "queued"
	// ReviewRunning is the reviewer at work.
	ReviewRunning ReviewTaskState = "running"
	// ReviewSucceeded ended with the reviewer's review submitted.
	ReviewSucceeded ReviewTaskState = "succeeded"
	// ReviewFailed ended without a review, for its Reason.
	ReviewFailed ReviewTaskState = "failed"
	// ReviewCancelled ended without a review: a newer push, the branch's deletion or a request for another review
	// superseded it, or someone cancelled it.
	ReviewCancelled ReviewTaskState = "cancelled"
)

// Open reports whether a review task in this state hasn't ended.
func (s ReviewTaskState) Open() bool {
	return s == ReviewWaiting || s == ReviewQueued || s == ReviewRunning
}

// AIReview is the AI reviewer's latest task for a branch's head: its number among its organization's tasks, its
// state and why it is there, for one that waits or ended without a review.
type AIReview struct {
	Task   int64
	State  ReviewTaskState
	Reason string
}

// ReviewInstructions is what a review task asks of the AI reviewer, beside its built-in instructions and the
// repository's.
func ReviewInstructions(branch, sha string) string {
	return fmt.Sprintf("Review the changes of branch %s at commit %s against %s, and submit one review of that commit.",
		branch, sha, git.DefaultBranch)
}

// ReviewTitle is a review task's title: the branch and its head, within the 200 characters a title holds.
func ReviewTitle(branch, sha string) string {
	title := "Review " + branch + " at " + sha[:min(len(sha), 12)]
	if utf8.RuneCountInString(title) > 200 {
		title = string([]rune(title)[:197]) + "..."
	}
	return title
}

// RequestReview has the organization's AI reviewer review the branch's head again, for v, who holds repo.push on the
// repository: it supersedes the branch's open review task, and starts one even for a head pushed from outside the
// organization. It answers the new task; ErrReviewOff for the default branch or one the repository's settings leave
// out, ErrReviewerNoModel or ErrReviewerSuspended while the reviewer can't review, and repos.ErrNotFound without the
// branch.
func (s *Service) RequestReview(ctx context.Context, v Viewer, repoID uuid.UUID, branch string) (AIReview, error) {
	ctx, span := tracer.Start(ctx, "code.Service.RequestReview")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", branch))
	held, err := s.holds(ctx, v, repoID)
	if err != nil {
		return AIReview{}, failed(span, "requesting a review", err)
	}
	if !slices.Contains(held, repos.PermPush) {
		return AIReview{}, fmt.Errorf("%w: %s on the repository", auth.ErrForbidden, repos.PermPush)
	}
	if branch == git.DefaultBranch {
		return AIReview{}, ErrReviewOff
	}
	r, err := s.store.RequestReview(ctx, v.ID, repoID, branch)
	if err != nil {
		return AIReview{}, failed(span, "requesting a review", err)
	}
	return r, nil
}
