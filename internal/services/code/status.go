package code

import (
	"errors"
	"slices"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/git"
)

// BranchStatus is where a branch stands as a request to merge into its base
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/branch-status.md, "Branch status").
type BranchStatus string

// The five statuses of a branch, in the order Evaluate tries them.
const (
	// StatusProcessing is a branch whose push the push worker hasn't processed, or whose head carries a required
	// annotation that is Pending or In progress.
	StatusProcessing BranchStatus = "processing"
	// StatusBlocked is a branch with commits to merge that a requirement holds back.
	StatusBlocked BranchStatus = "blocked"
	// StatusEmpty is a branch with nothing to merge, whose head no merge took into its base.
	StatusEmpty BranchStatus = "empty"
	// StatusMerged is a branch whose head a merge took into its base.
	StatusMerged BranchStatus = "merged"
	// StatusReady is a branch with commits to merge and nothing that holds them back.
	StatusReady BranchStatus = "ready"
)

// CheckKind is what a branch's check is of.
type CheckKind string

// The kinds of checks.
const (
	// CheckAnnotation is an annotation the base requires on the branch's head, Successful.
	CheckAnnotation CheckKind = "annotation"
	// CheckLinearHistory is the base requiring that no commit to merge is a merge.
	CheckLinearHistory CheckKind = "linear_history"
	// CheckApprovals is the base requiring people's approvals of the branch's head.
	CheckApprovals CheckKind = "approvals"
	// CheckChangesRequested is a reviewer whose latest review of the branch's head requests changes, where the base
	// requires reviews.
	CheckChangesRequested CheckKind = "changes_requested"
	// CheckAI is the base requiring the AI reviewer's approval of the branch's head.
	CheckAI CheckKind = "ai"
	// CheckUpToDate is the base's tip being in the branch.
	CheckUpToDate CheckKind = "up_to_date"
)

// AIState is how the AI reviewer's check of a branch's head stands.
type AIState string

// The states of the AI reviewer's check.
const (
	// AIWaiting is its review task waiting for a member to start it.
	AIWaiting AIState = "waiting"
	// AIQueued is its review task waiting to run.
	AIQueued AIState = "queued"
	// AIRunning is the reviewer at work.
	AIRunning AIState = "running"
	// AIApproved is its latest review approving the head, the one state that meets the check.
	AIApproved AIState = "approved"
	// AIChangesRequested is its latest review requesting changes.
	AIChangesRequested AIState = "changes_requested"
	// AINotApproved is its review of the head ending without approving it.
	AINotApproved AIState = "not_approved"
	// AIUnreviewed is a head with neither a review task nor a review of the reviewer's.
	AIUnreviewed AIState = "unreviewed"
	// AIFailed is its review task failing, for the check's Reason.
	AIFailed AIState = "failed"
	// AICancelled is its review task cancelled, for the check's Reason.
	AICancelled AIState = "cancelled"
	// AINoModel is a reviewer that no admin gave a model.
	AINoModel AIState = "no_model"
	// AISuspended is a suspended reviewer.
	AISuspended AIState = "suspended"
	// AIOff is main's /cobranch-review.yaml turning the reviewer off, or leaving the branch out.
	AIOff AIState = "off"
	// AIMissing is an organization without its reviewer.
	AIMissing AIState = "missing"
)

// pending reports whether the AI reviewer's check waits for its review task.
func (s AIState) pending() bool {
	return s == AIWaiting || s == AIQueued || s == AIRunning
}

// Check is one of a branch's requirements and how it stands: for an annotation, its name and status, "" while the head
// carries none; for linear history, the merges among its commits to merge; for approvals, the approvals that count
// and the Required ones; for changes requested, its reviewer's name; for the AI reviewer, its state and why its review
// task failed or was cancelled; for up to date, the base's commits it lacks.
type Check struct {
	Kind     CheckKind
	Name     string
	Status   AnnotationStatus
	Count    int
	Required int
	AI       AIState
	Reason   string
	Met      bool
}

// AIReviewer is what the AI reviewer's check of a branch follows from: the reviewer, why it reviews none of the
// branch's pushes (ErrNoReviewer, ErrReviewerNoModel, ErrReviewerSuspended or ErrReviewOff; nil when it reviews
// them), and its latest review task of the branch's head, nil without one.
type AIReviewer struct {
	ID   uuid.UUID
	Off  error
	Task *AIReview
}

// Standing is what a branch's status follows from: whether it waits for the push worker, its base's requirements,
// what Git's objects say of it against its base, the annotations on its head, the reviews of its head in the order
// they were submitted and who is behind it (its author and the pusher who first brought it, whose approvals don't
// count), the AI reviewer where its base requires its approval, and whether a merge took its head into its base.
type Standing struct {
	Waiting      bool
	Requirements git.Requirements
	Facts        git.Facts
	Annotations  []Annotation
	Reviews      []Review
	Authors      []uuid.UUID
	Reviewer     AIReviewer
	Merged       bool
}

// Evaluate is a branch's status, the first of the five that holds, and the checks behind it: each annotation its base
// requires, linear history and reviews when required, and being up to date. A required annotation that runs, or the
// AI reviewer's open review task where its approval is required, keeps the branch Processing.
func Evaluate(s Standing) (BranchStatus, []Check) {
	checks := Checks(s)
	running := slices.ContainsFunc(checks, func(c Check) bool {
		return (c.Kind == CheckAnnotation && (c.Status == AnnotationPending || c.Status == AnnotationInProgress)) ||
			(c.Kind == CheckAI && c.AI.pending())
	})
	blocked := slices.ContainsFunc(checks, func(c Check) bool { return !c.Met })
	switch {
	case s.Waiting || running:
		return StatusProcessing, checks
	case s.Facts.Ahead > 0 && blocked:
		return StatusBlocked, checks
	case s.Facts.Ahead == 0 && !s.Merged:
		return StatusEmpty, checks
	case s.Facts.Ahead == 0:
		return StatusMerged, checks
	}
	return StatusReady, checks
}

// Checks lists how a branch meets its base's requirements: each required annotation on its head, Successful; no merge
// among its commits to merge, when linear history is required, which a branch with none to merge meets; when reviews
// are, the approvals required of its head, the AI reviewer's when required, and each reviewer requesting changes of
// it; and its base's tip in it.
func Checks(s Standing) []Check {
	checks := make([]Check, 0, len(s.Requirements.Annotations)+3)
	for _, name := range s.Requirements.Annotations {
		c := Check{Kind: CheckAnnotation, Name: name}
		if i := slices.IndexFunc(s.Annotations, func(a Annotation) bool { return a.Name == name }); i >= 0 {
			c.Status = s.Annotations[i].Status
		}
		c.Met = c.Status == AnnotationSuccessful
		checks = append(checks, c)
	}
	if s.Requirements.LinearHistory {
		c := Check{Kind: CheckLinearHistory, Met: s.Facts.Ahead == 0}
		if s.Facts.Merges != nil {
			c.Count = *s.Facts.Merges
			c.Met = c.Count == 0
		}
		checks = append(checks, c)
	}
	if s.Requirements.Reviews != nil {
		checks = append(checks, reviewChecks(*s.Requirements.Reviews, s.Reviews, s.Authors, s.Reviewer)...)
	}
	return append(checks, Check{Kind: CheckUpToDate, Count: s.Facts.Behind, Met: s.Facts.Behind == 0})
}

// reviewChecks lists how a head's reviews meet required: its approvals, when it requires any, counting each person's
// latest review that approves it, besides those of authors; the AI reviewer's approval, when it requires it; then each
// other reviewer whose latest review requests changes, in the order they first reviewed it.
func reviewChecks(required git.ReviewRequirements, reviews []Review, authors []uuid.UUID, reviewer AIReviewer) []Check {
	latest := Latest(reviews)
	var checks []Check
	if required.Approvals > 0 {
		approvals := 0
		for _, r := range latest {
			if r.State == ReviewApproved && r.Person && !slices.Contains(authors, r.Reviewer.ID) {
				approvals++
			}
		}
		checks = append(checks, Check{Kind: CheckApprovals, Count: approvals, Required: required.Approvals, Met: approvals >= required.Approvals})
	}
	if required.AI {
		checks = append(checks, aiCheck(latest, reviewer))
	}
	for _, r := range latest {
		if r.State == ReviewChangesRequested && (!required.AI || !isReviewer(r, reviewer.ID)) {
			checks = append(checks, Check{Kind: CheckChangesRequested, Name: r.Reviewer.DisplayName})
		}
	}
	return checks
}

// aiCheck is how the AI reviewer's approval of a head stands: pending while its review task for the head is open; then
// met where its latest review approves the head; otherwise why not, the review's verdict first, then its task's end,
// then the reviewer's own state.
func aiCheck(latest []Review, reviewer AIReviewer) Check {
	c := Check{Kind: CheckAI}
	task := reviewer.Task
	if task != nil && task.State.Open() {
		c.AI = AIState(task.State)
		return c
	}
	verdict := ReviewState("")
	if i := slices.IndexFunc(latest, func(r Review) bool { return isReviewer(r, reviewer.ID) }); i >= 0 {
		verdict = latest[i].State
	}
	switch {
	case verdict == ReviewApproved:
		c.AI, c.Met = AIApproved, true
	case verdict == ReviewChangesRequested:
		c.AI = AIChangesRequested
	case task != nil && (task.State == ReviewFailed || task.State == ReviewCancelled):
		c.AI, c.Reason = AIState(task.State), task.Reason
	case reviewer.Off != nil:
		c.AI = offState(reviewer.Off)
	case verdict != "" || task != nil:
		c.AI = AINotApproved
	default:
		c.AI = AIUnreviewed
	}
	return c
}

// isReviewer reports whether r is the AI reviewer's review.
func isReviewer(r Review, reviewer uuid.UUID) bool {
	return r.Reviewer != nil && reviewer != uuid.Nil && r.Reviewer.ID == reviewer
}

// offState is the AI reviewer's check for why it reviews none of a branch's pushes.
func offState(err error) AIState {
	switch {
	case errors.Is(err, ErrReviewerNoModel):
		return AINoModel
	case errors.Is(err, ErrReviewerSuspended):
		return AISuspended
	case errors.Is(err, ErrReviewOff):
		return AIOff
	}
	return AIMissing
}
