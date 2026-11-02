package gitserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/adapters/gitinternal"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

// mergeConflicts is how many of a conflicted merge's paths its refusal names.
const mergeConflicts = 5

// mergeHandler serves the internal call's merges: each builds the merge it asks for in a throwaway repository of one
// snapshot and records it as a push of its base for its requester, transport api, through the same transaction and
// checks as a client's push (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/branch-status.md,
// "By asking Cobranch"). Its requester is the viewer whose token the caller sends on, who must hold repo.merge, and
// repo.bypass for a merge past main's requirements or of a branch that isn't Ready. It answers 200 once the push is
// committed, 403 for a merge the viewer may not ask for, 404 for a repository they can't see, and 409 for a merge
// Cobranch won't make.
func (s *Server) mergeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		viewer, ok := s.internalViewer(w, r)
		if !ok {
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		var body gitinternal.MergeRequest
		if err == nil {
			err = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
		}
		req := body.Domain()
		if err != nil || validMerge(req) != nil {
			problem(w, http.StatusBadRequest, gitinternal.Problem{Message: "a repository's ID, and a merge of a branch into main"})
			return
		}
		ctx, span := tracer.Start(auth.WithViewer(r.Context(), viewer), "gitserver.internal merge", trace.WithAttributes(
			attribute.String("cobranch.repository.id", id.String()), attribute.String("cobranch.branch", req.Branch),
			attribute.String("cobranch.merge.method", string(req.Method))))
		defer span.End()
		merged, err := s.mergeFor(ctx, viewer.User, id, req)
		if err != nil {
			answerMerge(ctx, w, span, id, req.Branch, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gitinternal.MergedOf(merged)) // a caller gone away gets nothing more
	}
}

// mergeFor makes req's merge in repository repoID once viewer may ask for it: they see the repository, hold repo.merge,
// and repo.bypass for a merge past main's requirements, and merge as themselves.
func (s *Server) mergeFor(ctx context.Context, viewer auth.User, repoID uuid.UUID, req code.MergeRequest) (code.Merged, error) {
	_, held, err := s.repos.ByID(ctx, viewer, repoID)
	switch {
	case err != nil:
		return code.Merged{}, fmt.Errorf("finding repository %s for its merge: %w", repoID, err)
	case req.By.ID != viewer.ID:
		return code.Merged{}, fmt.Errorf("%w: a merge only for the viewer who asks for it", auth.ErrForbidden)
	case !slices.Contains(held, repos.PermMerge):
		return code.Merged{}, fmt.Errorf("%w: %s on the repository", auth.ErrForbidden, repos.PermMerge)
	case req.Bypass && !slices.Contains(held, repos.PermBypass):
		return code.Merged{}, fmt.Errorf("%w: %s on the repository", auth.ErrForbidden, repos.PermBypass)
	}
	return s.merge(ctx, repoID, req)
}

// answerMerge answers a merge that failed: 403 for one the viewer may not ask for, 404 with the code of what it didn't
// find, 409 for one Cobranch won't make, or 500, logged, for anything else.
func answerMerge(ctx context.Context, w http.ResponseWriter, span trace.Span, repoID uuid.UUID, branch string, err error) {
	var refused *code.MergeError
	c, missing := gitinternal.CodeOf(err)
	switch {
	case errors.Is(err, auth.ErrForbidden):
		problem(w, http.StatusForbidden, gitinternal.Problem{Message: err.Error()})
	case missing:
		problem(w, http.StatusNotFound, gitinternal.Problem{Code: c, Message: err.Error()})
	case errors.Is(err, code.ErrBranchMoved):
		problem(w, http.StatusConflict, gitinternal.Problem{Code: gitinternal.CodeMoved, Message: code.ErrBranchMoved.Error()})
	case errors.As(err, &refused):
		problem(w, http.StatusConflict, gitinternal.Problem{Code: refused.Code, Message: refused.Message})
	default:
		span.SetStatus(codes.Error, err.Error())
		slog.ErrorContext(ctx, "Merging a branch failed", "repository", repoID, "branch", branch, "error", err)
		problem(w, http.StatusInternalServerError, gitinternal.Problem{Message: "the merge could not be made"})
	}
}

// validMerge checks a merge the internal call asks for: of a branch at a commit into main, by a method there is, for
// a principal with a name.
func validMerge(req code.MergeRequest) error {
	switch {
	case code.ValidateRef(req.Branch) != nil, req.Branch == git.DefaultBranch, code.ValidateSHA("head", req.Head) != nil:
		return errors.New("a branch, besides main, at a commit")
	case req.Base != git.DefaultBranch:
		return errors.New("a merge into main")
	case req.Method != code.MethodMerge && req.Method != code.MethodFastForward:
		return errors.New("a merge's method")
	case req.By.ID == uuid.Nil || req.By.Name == "":
		return errors.New("a principal with a name")
	}
	return nil
}

// merge builds the merge req asks for over a snapshot of the repository read now, and records it as a push of its
// base.
func (s *Server) merge(ctx context.Context, repoID uuid.UUID, req code.MergeRequest) (code.Merged, error) {
	dir, remove, err := s.snapshotWorkspace(ctx, repoID, opMerge)
	if err != nil {
		return code.Merged{}, err
	}
	defer remove()
	base, err := s.mergeTips(ctx, dir, req)
	if err != nil {
		return code.Merged{}, err
	}
	commit, err := s.mergeCommit(ctx, dir, req, base)
	if err != nil {
		return code.Merged{}, err
	}
	push, err := s.mergePush(ctx, dir, repoID, req, git.RefUpdate{Name: "refs/heads/" + req.Base, Old: base, New: commit})
	if err != nil {
		return code.Merged{}, err
	}
	answer, err := s.recorder.Record(ctx, push)
	switch {
	case err != nil:
		return code.Merged{}, fmt.Errorf("recording the merge: %w", err)
	case answer.Stale:
		return code.Merged{}, code.ErrBranchMoved
	case !answer.Accepted:
		return code.Merged{}, &code.MergeError{Code: code.MergeRefused, Message: answer.Message}
	}
	return code.Merged{Base: req.Base, Head: req.Head, Commit: commit}, nil
}

// mergeTips returns the commit req's base is at in the repository at dir, once its branch proves to be at req's head:
// code.ErrBranchMoved when it isn't, or the branch or its base is gone.
func (s *Server) mergeTips(ctx context.Context, dir string, req code.MergeRequest) (string, error) {
	listed, err := s.read(ctx, dir, nil, "for-each-ref", "--format=%(objectname) %(refname)", "refs/heads/")
	if err != nil {
		return "", err
	}
	tips := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(listed)), "\n") {
		if sha, ref, ok := strings.Cut(line, " "); ok {
			tips[ref] = sha
		}
	}
	base, head := tips["refs/heads/"+req.Base], tips["refs/heads/"+req.Branch]
	if base == "" || head != req.Head {
		return "", code.ErrBranchMoved
	}
	return base, nil
}

// mergeCommit returns the commit req's merge moves its base to, from base: for a fast-forward, the head, once base
// proves to be in it; otherwise a new merge commit of the two, authored and committed by req's principal now.
func (s *Server) mergeCommit(ctx context.Context, dir string, req code.MergeRequest, base string) (string, error) {
	merged, err := s.isAncestor(ctx, dir, req.Head, base)
	switch {
	case err != nil:
		return "", err
	case merged:
		return "", &code.MergeError{Code: code.MergeNothing, Message: req.Branch + " has no commits " + req.Base + " lacks"}
	}
	if req.Method == code.MethodFastForward {
		forward, err := s.isAncestor(ctx, dir, base, req.Head)
		switch {
		case err != nil:
			return "", err
		case !forward:
			return "", fmt.Errorf("fast-forwarding %s: %w", req.Base, code.NotFastForward(req.Branch, req.Base, 0))
		}
		return req.Head, nil
	}
	tree, err := s.mergeTree(ctx, dir, req, base)
	if err != nil {
		return "", err
	}
	at := "@" + strconv.FormatInt(time.Now().Unix(), 10) + " +0000"
	cmd := s.git(ctx, []string{"GIT_DIR=" + dir, "GIT_AUTHOR_NAME=" + req.By.Name, "GIT_AUTHOR_EMAIL=" + req.By.Email, "GIT_AUTHOR_DATE=" + at,
		"GIT_COMMITTER_NAME=" + req.By.Name, "GIT_COMMITTER_EMAIL=" + req.By.Email, "GIT_COMMITTER_DATE=" + at},
		"commit-tree", tree, "-p", base, "-p", req.Head, "-m", "Merge branch '"+req.Branch+"'")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("writing the merge commit: %w%s", err, stderrOf(err))
	}
	return strings.TrimSpace(string(out)), nil
}

// isAncestor reports whether commit a is in commit b's history, b itself included.
func (s *Server) isAncestor(ctx context.Context, dir, a, b string) (bool, error) {
	err := s.objectsGit(ctx, dir, "merge-base", "--is-ancestor", a, b).Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	}
	return false, fmt.Errorf("asking whether %s is in %s's history: %w", a, b, err)
}

// mergeTree writes the tree of the merge of req's head into base, and returns it, or a *code.MergeError naming the
// paths the two change in ways Git can't merge.
func (s *Server) mergeTree(ctx context.Context, dir string, req code.MergeRequest, base string) (string, error) {
	tree, paths, err := s.mergeTreeOf(ctx, dir, nil, base, req.Head)
	switch {
	case err != nil:
		return "", err
	case len(paths) == 0:
		return tree, nil
	}
	named := strings.Join(paths[:min(len(paths), mergeConflicts)], ", ")
	if more := len(paths) - mergeConflicts; more > 0 {
		named += " and " + strconv.Itoa(more) + " more"
	}
	return "", &code.MergeError{Code: code.MergeConflict, Message: req.Branch + " and " + req.Base + " both change " + named +
		": merge " + req.Base + " into " + req.Branch + ", resolve them, and push it again"}
}

// mergePush is the push that records req's merge, update: its commit, what its refs say of the branches, and its
// pack, uploaded; a merge whose base's new /cobranch.yaml doesn't parse is a *code.MergeError.
func (s *Server) mergePush(ctx context.Context, dir string, repoID uuid.UUID, req code.MergeRequest, update git.RefUpdate) (git.Push, error) {
	updates := []git.RefUpdate{update}
	commits, err := s.newCommits(ctx, dir, nil, updates)
	if err != nil {
		return git.Push{}, err
	}
	branches, refusal, err := s.branchFacts(ctx, dir, nil, updates)
	if err != nil {
		return git.Push{}, err
	}
	if refusal != "" {
		return git.Push{}, &code.MergeError{Code: code.MergeRefused, Message: refusal}
	}
	var pack *git.Pack
	if len(commits) > 0 {
		if pack, err = s.mergePack(ctx, dir, repoID, update.New, update.Old, req.Head); err != nil {
			return git.Push{}, err
		}
	}
	return git.Push{ID: uuid.Must(uuid.NewV7()), RepoID: repoID, PusherID: req.By.ID, Transport: git.TransportAPI, Updates: updates,
		Pack: pack, Commits: commits, ReceivedAt: time.Now(), Requirements: branches.requirements, Facts: branches.facts,
		Tips: branches.tips, Brought: branches.brought, Bypass: req.Bypass, Merging: &git.Head{Branch: req.Branch, SHA: req.Head}}, nil
}

// mergePack packs the objects merge commit brings that its parents lack, and uploads the pack.
func (s *Server) mergePack(ctx context.Context, dir string, repoID uuid.UUID, commit string, parents ...string) (*git.Pack, error) {
	out := filepath.Join(dir, "merge")
	if err := os.Mkdir(out, 0o700); err != nil {
		return nil, fmt.Errorf("making the merge's pack directory: %w", err)
	}
	revs := commit + "\n"
	for _, p := range parents {
		revs += "^" + p + "\n"
	}
	hash, err := s.read(ctx, dir, strings.NewReader(revs), "pack-objects", "--revs", "-q", filepath.Join(out, "pack"))
	if err != nil {
		return nil, err
	}
	name := "pack-" + strings.TrimSpace(string(hash))
	return s.keepPack(ctx, repoID, name, func(file string) string { return filepath.Join(out, file) })
}

// stderrOf is what a git command that failed printed on its standard error, after a colon, or "".
func stderrOf(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		return ": " + strings.TrimSpace(string(exit.Stderr))
	}
	return ""
}
