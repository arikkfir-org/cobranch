package code

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

// DeleteRequest is a branch deletion the Git server records as a push for By: Branch, from Head to nothing, past its
// merge requirements when Bypass.
type DeleteRequest struct {
	Branch string
	Head   string
	By     uuid.UUID
	Bypass bool
}

// DeletedBranch is the latest deletion of a repository's branch of a name: the head it was at, that commit when a
// push recorded it, when it was created and deleted, who deleted it, nil when no one was recorded or they are gone,
// and its last merge, nil when none was recorded.
type DeletedBranch struct {
	Name      string
	Head      string
	Commit    *Commit
	CreatedAt time.Time
	DeletedAt time.Time
	DeletedBy *Principal
	Merge     *Merge
}

// DeleteError is a branch deletion Cobranch won't make, with the code and the words its requester sees.
type DeleteError struct {
	Code    string
	Message string
}

func (e *DeleteError) Error() string {
	return e.Message
}

// The codes of the deletions Cobranch won't make.
const (
	// DeleteDefault is a deletion of the default branch, which every other merges into.
	DeleteDefault = "branch.default"
	// DeleteRefused is a deletion that the push recording it breaks a rule of, such as a branch only Cobranch writes.
	DeleteRefused = "branch.refused"
)

// DeleteBranch deletes one of the repository's branches for v, at head, the head they saw, or wherever it is for "",
// and answers once the Git server recorded it as a push that deletes its ref
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/branch-status.md, "Deleting a branch"). It
// takes repo.push, and repo.bypass for a branch the default branch's /cobranch.yaml names. The default branch is never
// deleted. A head the branch has moved from, or a branch that moved while the Git server recorded it, is
// ErrBranchMoved; for "", a branch that moved meanwhile is read again and deleted at its new head, up to
// deleteAttempts times, and is ErrBranchMoved only once it kept moving. A deletion Cobranch won't make is a
// *DeleteError.
func (s *Service) DeleteBranch(ctx context.Context, v Viewer, repoID uuid.UUID, name, head string) error {
	ctx, span := tracer.Start(ctx, "code.Service.DeleteBranch")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", name))
	held, err := s.holds(ctx, v, repoID)
	if err != nil {
		return failed(span, "deleting the branch", err)
	}
	if !slices.Contains(held, repos.PermPush) {
		return fmt.Errorf("%w: %s on the repository", auth.ErrForbidden, repos.PermPush)
	}
	if head != "" {
		if err := ValidateSHA("head", head); err != nil {
			return err
		}
	}
	bypass := slices.Contains(held, repos.PermBypass)
	err = s.deleteAt(ctx, v.ID, repoID, name, head, bypass)
	for attempt := 1; head == "" && attempt < deleteAttempts && errors.Is(err, ErrBranchMoved); attempt++ {
		err = s.deleteAt(ctx, v.ID, repoID, name, head, bypass)
	}
	var refused *DeleteError
	if errors.As(err, &refused) || errors.Is(err, ErrBranchMoved) {
		return fmt.Errorf("deleting %s: %w", name, err)
	}
	return failed(span, "deleting the branch", err)
}

// deleteAttempts is how many times DeleteBranch asks the Git server to delete a branch wherever it is, each time at the
// head it reads then, before it answers that the branch kept moving.
const deleteAttempts = 3

// deleteAt asks the Git server to delete the branch of name for by, as the store has it now, at head, or at the head
// the store has for "".
func (s *Service) deleteAt(ctx context.Context, by, repoID uuid.UUID, name, head string, bypass bool) error {
	b, err := s.store.Branch(ctx, repoID, name)
	if err != nil {
		return fmt.Errorf("reading the branch: %w", err)
	}
	req, err := deleteRequest(b, head, bypass)
	if err != nil {
		return err
	}
	req.By = by
	if err := s.objects.DeleteBranch(ctx, repoID, req); err != nil {
		return fmt.Errorf("at %s: %w", req.Head, err)
	}
	return nil
}

// deleteRequest is the deletion the Git server records of b, as it stands, at head: refused when b is the default
// branch, has moved from head, or has merge requirements and the requester may not bypass them.
func deleteRequest(b Branch, head string, bypass bool) (DeleteRequest, error) {
	switch {
	case b.Name == git.DefaultBranch:
		return DeleteRequest{}, &DeleteError{Code: DeleteDefault, Message: b.Name + " is the branch every other merges into: it is never deleted"}
	case head != "" && head != b.Head:
		return DeleteRequest{}, ErrBranchMoved
	case b.Requirements != nil && !bypass:
		return DeleteRequest{}, fmt.Errorf("%w: %s on the repository, to delete %s, which /cobranch.yaml names", auth.ErrForbidden, repos.PermBypass, b.Name)
	}
	return DeleteRequest{Branch: b.Name, Head: b.Head, Bypass: b.Requirements != nil}, nil
}

// DeletedBranch reads the latest deletion of the repository's branch of name, which no live branch has; it takes
// repo.read, and answers repos.ErrNotFound for a name no deleted branch had, or a live branch has.
func (s *Service) DeletedBranch(ctx context.Context, v Viewer, repoID uuid.UUID, name string) (DeletedBranch, error) {
	ctx, span := tracer.Start(ctx, "code.Service.DeletedBranch")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", name))
	if err := s.read(ctx, v, repoID); err != nil {
		return DeletedBranch{}, failed(span, "reading the deleted branch", err)
	}
	b, err := s.store.DeletedBranch(ctx, repoID, name)
	if err != nil {
		return DeletedBranch{}, failed(span, "reading the deleted branch", err)
	}
	return b, nil
}
