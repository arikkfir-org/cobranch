package code

import (
	"slices"

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
	// CheckUpToDate is the base's tip being in the branch.
	CheckUpToDate CheckKind = "up_to_date"
)

// Check is one of a branch's requirements and how it stands: for an annotation, its name and status, "" while the head
// carries none; for linear history, the merges among its commits to merge; for up to date, the base's commits it
// lacks.
type Check struct {
	Kind   CheckKind
	Name   string
	Status AnnotationStatus
	Count  int
	Met    bool
}

// Standing is what a branch's status follows from: whether it waits for the push worker, its base's requirements,
// what Git's objects say of it against its base, the annotations on its head, and whether a merge took its head into
// its base.
type Standing struct {
	Waiting      bool
	Requirements git.Requirements
	Facts        git.Facts
	Annotations  []Annotation
	Merged       bool
}

// Evaluate is a branch's status, the first of the five that holds, and the checks behind it: each annotation its base
// requires, linear history when required, and being up to date.
func Evaluate(s Standing) (BranchStatus, []Check) {
	checks := Checks(s)
	running := slices.ContainsFunc(checks, func(c Check) bool {
		return c.Kind == CheckAnnotation && (c.Status == AnnotationPending || c.Status == AnnotationInProgress)
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
// among its commits to merge, when linear history is required, which a branch with none to merge meets; and its base's
// tip in it.
func Checks(s Standing) []Check {
	checks := make([]Check, 0, len(s.Requirements.Annotations)+2)
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
	return append(checks, Check{Kind: CheckUpToDate, Count: s.Facts.Behind, Met: s.Facts.Behind == 0})
}
