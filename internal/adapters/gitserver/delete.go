package gitserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/adapters/gitinternal"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

// deleteHandler serves the internal call's branch deletions: each records a push of one ref update, the branch from
// its head to nothing, for its requester, transport api, through the same transaction and checks as a client's push
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/branch-status.md, "Deleting a branch"). It
// reads no snapshot: a deletion brings no objects and changes no other branch's facts. Its requester is the viewer
// whose token the caller sends on, who must hold repo.push, and repo.bypass for a branch with merge requirements. It
// answers 204 once the push is committed, 403 for a deletion the viewer may not ask for, 404 for a repository they
// can't see, and 409 for a branch that moved or a deletion Cobranch won't make.
func (s *Server) deleteHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		viewer, ok := s.internalViewer(w, r)
		if !ok {
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		var body gitinternal.DeleteRequest
		if err == nil {
			err = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
		}
		req := body.Domain()
		if err != nil || validDelete(req) != nil {
			problem(w, http.StatusBadRequest, gitinternal.Problem{Message: "a repository's ID, and a deletion of a branch besides main at a commit"})
			return
		}
		ctx, span := tracer.Start(auth.WithViewer(r.Context(), viewer), "gitserver.internal delete", trace.WithAttributes(
			attribute.String("cobranch.repository.id", id.String()), attribute.String("cobranch.branch", req.Branch)))
		defer span.End()
		if err := s.deleteFor(ctx, viewer.User, id, req); err != nil {
			answerChange(ctx, w, span, "deletion", id, req.Branch, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// validDelete checks a deletion the internal call asks for: of a branch besides main, at a commit, for a principal.
func validDelete(req code.DeleteRequest) error {
	switch {
	case code.ValidateRef(req.Branch) != nil, req.Branch == git.DefaultBranch, code.ValidateSHA("head", req.Head) != nil:
		return errors.New("a branch, besides main, at a commit")
	case req.By == uuid.Nil:
		return errors.New("a principal")
	}
	return nil
}

// deleteFor records req's deletion in repository repoID once viewer may ask for it: they see the repository, hold
// repo.push, and repo.bypass for a deletion past the branch's requirements, and delete as themselves.
func (s *Server) deleteFor(ctx context.Context, viewer auth.User, repoID uuid.UUID, req code.DeleteRequest) error {
	_, held, err := s.repos.ByID(ctx, viewer, repoID)
	switch {
	case err != nil:
		return fmt.Errorf("finding repository %s for its deletion: %w", repoID, err)
	case req.By != viewer.ID:
		return fmt.Errorf("%w: a deletion only for the viewer who asks for it", auth.ErrForbidden)
	case !slices.Contains(held, repos.PermPush):
		return fmt.Errorf("%w: %s on the repository", auth.ErrForbidden, repos.PermPush)
	case req.Bypass && !slices.Contains(held, repos.PermBypass):
		return fmt.Errorf("%w: %s on the repository", auth.ErrForbidden, repos.PermBypass)
	}
	return s.deleteBranch(ctx, repoID, req)
}

// deleteBranch records req's deletion as a push that deletes its branch's ref.
func (s *Server) deleteBranch(ctx context.Context, repoID uuid.UUID, req code.DeleteRequest) error {
	push := git.Push{ID: uuid.Must(uuid.NewV7()), RepoID: repoID, PusherID: req.By, Transport: git.TransportAPI,
		Updates: []git.RefUpdate{{Name: "refs/heads/" + req.Branch, Old: req.Head, New: git.ZeroSHA}}, ReceivedAt: time.Now(), Bypass: req.Bypass}
	answer, err := s.recorder.Record(ctx, push)
	switch {
	case err != nil:
		return fmt.Errorf("recording the deletion: %w", err)
	case answer.Stale:
		return code.ErrBranchMoved
	case !answer.Accepted:
		return &code.DeleteError{Code: code.DeleteRefused, Message: answer.Message}
	}
	return nil
}
