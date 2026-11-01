package code

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// MergeMethod is how a merge asked of Cobranch moves its base: with a merge commit, or by a fast-forward, which writes
// nothing.
type MergeMethod string

// The merge methods.
const (
	MethodMerge       MergeMethod = "merge"
	MethodFastForward MergeMethod = "fast_forward"
)

// MergeInput is what a principal asks of a merge: the head they saw, which refuses the merge once the branch moved
// from it, "" for whatever it is at; and the method, "" for its base's: a fast-forward where the base requires linear
// history, a merge commit otherwise.
type MergeInput struct {
	Head   string
	Method MergeMethod
}

// MergeRequest is a merge the Git server builds and records as a push of Base for By: Branch, at Head, into Base by
// Method, past Base's merge requirements when Bypass.
type MergeRequest struct {
	Branch string
	Head   string
	Base   string
	Method MergeMethod
	By     Merger
	Bypass bool
}

// Merger is who asks for a merge, as its commit writes them: their name, and their verified email, "" for a principal
// that holds none.
type Merger struct {
	ID    uuid.UUID
	Name  string
	Email string
}

// Merged is a merge the Git server recorded: the branch it merged into, the head it merged, and the commit the base
// is at now, the merge commit or, for a fast-forward, the head.
type Merged struct {
	Base   string
	Head   string
	Commit string
}

// ErrBranchMoved is a merge of a branch that moved, or whose base moved, since its requester read them.
var ErrBranchMoved = errors.New("the branch or its base moved since you read them: read them again")

// MergeError is a merge Cobranch won't make, with the code and the words its requester sees.
type MergeError struct {
	Code    string
	Message string
}

func (e *MergeError) Error() string {
	return e.Message
}

// The codes of the merges Cobranch won't make.
const (
	// MergeNotReady is a branch that isn't Ready, merged without repo.bypass.
	MergeNotReady = "branch.not_ready"
	// MergeNothing is a branch with no commits its base lacks.
	MergeNothing = "branch.nothing_to_merge"
	// MergeNotFastForward is a fast-forward of a branch that lacks some of its base's commits.
	MergeNotFastForward = "merge.not_fast_forward"
	// MergeConflict is a merge commit of a branch and a base that change the same lines.
	MergeConflict = "merge.conflict"
	// MergeRefused is a merge that the push recording it breaks a rule of, such as a /cobranch.yaml that doesn't parse.
	MergeRefused = "merge.refused"
)

// Merge merges one of the repository's branches into its base, main, for v, and answers once the Git server recorded
// it as a push of main (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/branch-status.md, "By
// asking Cobranch"). It takes repo.merge, and repo.bypass for a branch that isn't Ready, which it also passes the
// requirements of main's /cobranch.yaml. A head the branch has moved from, or a branch or a main that moved while the
// Git server built the merge, is ErrBranchMoved; a merge Cobranch won't make is a *MergeError.
func (s *Service) Merge(ctx context.Context, v Viewer, repoID uuid.UUID, name string, in MergeInput) (Merged, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Merge")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", name),
		attribute.String("cobranch.merge.method", string(in.Method)))
	held, err := s.holds(ctx, v, repoID)
	if err != nil {
		return Merged{}, failed(span, "merging the branch", err)
	}
	if !slices.Contains(held, repos.PermMerge) {
		return Merged{}, fmt.Errorf("%w: %s on the repository", auth.ErrForbidden, repos.PermMerge)
	}
	if err := validateMerge(in); err != nil {
		return Merged{}, err
	}
	b, err := s.store.Branch(ctx, repoID, name)
	if err != nil {
		return Merged{}, failed(span, "merging the branch", err)
	}
	linear, err := s.linearHistory(ctx, repoID, b)
	if err != nil {
		return Merged{}, failed(span, "merging the branch", err)
	}
	req, err := mergeRequest(b, in, linear, slices.Contains(held, repos.PermBypass))
	if err != nil {
		return Merged{}, err
	}
	req.By = merger(v)
	merged, err := s.objects.Merge(ctx, repoID, req)
	var refused *MergeError
	if errors.As(err, &refused) || errors.Is(err, ErrBranchMoved) {
		return Merged{}, fmt.Errorf("merging %s: %w", name, err)
	}
	if err != nil {
		return Merged{}, failed(span, "merging the branch", err)
	}
	return merged, nil
}

// validateMerge checks what a merge's input says: a head that names an object, and a method there is.
func validateMerge(in MergeInput) error {
	if in.Head != "" {
		if err := ValidateSHA("head", in.Head); err != nil {
			return err
		}
	}
	if in.Method != "" && in.Method != MethodMerge && in.Method != MethodFastForward {
		return &orgs.ValidationError{Field: "method", Code: "method.invalid", Message: "merge or fast_forward"}
	}
	return nil
}

// linearHistory reports whether b's base requires linear history; false for the default branch, which has none.
func (s *Service) linearHistory(ctx context.Context, repoID uuid.UUID, b Branch) (bool, error) {
	if b.Base == "" {
		return false, nil
	}
	base, err := s.store.Branch(ctx, repoID, b.Base)
	if errors.Is(err, repos.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading branch %s: %w", b.Base, err)
	}
	return base.Requirements != nil && base.Requirements.LinearHistory, nil
}

// mergeRequest is the merge the Git server builds of b, as it stands, for in: refused when b is the default branch,
// has moved from in's head, has nothing to merge, isn't Ready without bypass, asks for a merge commit where its base
// requires linear history, or for a fast-forward while behind its base.
func mergeRequest(b Branch, in MergeInput, linear, bypass bool) (MergeRequest, error) {
	if b.Base == "" {
		return MergeRequest{}, &orgs.ValidationError{Field: "branch", Code: "branch.default", Message: b.Name + " merges into no branch"}
	}
	if in.Head != "" && in.Head != b.Head {
		return MergeRequest{}, ErrBranchMoved
	}
	method := in.Method
	switch {
	case method == "" && linear:
		method = MethodFastForward
	case method == "":
		method = MethodMerge
	case method == MethodMerge && linear:
		return MergeRequest{}, &orgs.ValidationError{Field: "method", Code: "method.invalid",
			Message: b.Base + " requires linear history: merge by fast_forward"}
	}
	if b.Status == StatusEmpty || b.Status == StatusMerged || (b.Facts != nil && b.Facts.Ahead == 0) {
		return MergeRequest{}, &MergeError{Code: MergeNothing, Message: b.Name + " has no commits " + b.Base + " lacks"}
	}
	if why := notReady(git.Head{Branch: b.Name, SHA: b.Head}, incoming(b), true); why != "" && !bypass {
		return MergeRequest{}, &MergeError{Code: MergeNotReady, Message: b.Name + " isn't Ready: " + why}
	}
	if method == MethodFastForward && b.Facts != nil && b.Facts.Behind > 0 {
		return MergeRequest{}, NotFastForward(b.Name, b.Base, b.Facts.Behind)
	}
	return MergeRequest{Branch: b.Name, Head: b.Head, Base: b.Base, Method: method, Bypass: bypass}, nil
}

// incoming is b as a merge's checks read it.
func incoming(b Branch) Incoming {
	return Incoming{Head: b.Head, Base: b.Base, Status: b.Status, Checks: b.Checks}
}

// NotFastForward is the refusal of a fast-forward of branch that lacks behind of base's commits; behind 0 for a
// count nobody worked out. The Git server, which finds it out from Git's objects, refuses with it too.
func NotFastForward(branch, base string, behind int) *MergeError {
	lacks := "lacks commits of " + base
	if behind > 0 {
		lacks = "is " + plural(behind, "commit") + " behind " + base
	}
	return &MergeError{Code: MergeNotFastForward, Message: branch + " " + lacks + ", so it can't fast-forward: merge " + base +
		" into it, or rebase it, and push it again"}
}

// merger is v as a merge's commit writes them: their display name, and the first email they hold verified.
func merger(v Viewer) Merger {
	m := Merger{ID: v.ID, Name: v.DisplayName}
	for _, e := range v.Emails {
		if e.Verified {
			m.Email = e.Email
			break
		}
	}
	return m
}
