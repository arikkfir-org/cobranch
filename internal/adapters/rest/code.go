package rest

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

// Code is what the API needs of the code's service.
type Code interface {
	Branches(ctx context.Context, v code.Viewer, repoID uuid.UUID) ([]code.Branch, error)
	Branch(ctx context.Context, v code.Viewer, repoID uuid.UUID, name string) (code.Branch, error)
	History(ctx context.Context, v code.Viewer, repoID uuid.UUID, branch, cursor string, limit int) (code.Page, error)
	Commit(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha string) (code.Commit, code.Push, error)
	Annotations(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha string) ([]code.Annotation, error)
	SetAnnotation(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha string, in code.AnnotationInput) (code.Annotation, error)
	DeleteAnnotation(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha, name string, version int64) error
	Tags(ctx context.Context, v code.Viewer, repoID uuid.UUID) ([]code.Tag, error)
	Tree(ctx context.Context, v code.Viewer, repoID uuid.UUID, ref, path string) (code.Tree, error)
	File(ctx context.Context, v code.Viewer, repoID uuid.UUID, ref, path string) (code.File, error)
	Raw(ctx context.Context, v code.Viewer, repoID uuid.UUID, ref, path string) (code.Raw, error)
	Merge(ctx context.Context, v code.Viewer, repoID uuid.UUID, name string, in code.MergeInput) (code.Merged, error)
}

func principal(p *code.Principal) *Principal {
	if p == nil {
		return nil
	}
	return &Principal{Id: p.ID, DisplayName: p.DisplayName}
}

func person(p code.Person) Person {
	return Person{Name: p.Name, Email: p.Email, At: p.At.UTC(), Zone: p.Zone, Principal: principal(p.Principal)}
}

// commit is c as the API shows it, with its annotations; a list's commits come without their pushes.
func commit(c code.Commit) Commit {
	return Commit{Sha: c.SHA, Tree: c.Tree, Parents: nonNil(c.Parents), Message: c.Message, Author: person(c.Author),
		Committer: person(c.Committer), Signature: c.Signature, Annotations: mapAll(c.Annotations, annotation)}
}

func annotation(a code.Annotation) Annotation {
	out := Annotation{Id: a.ID, Name: a.Name, Status: AnnotationStatus(a.Status), Summary: a.Summary, Link: a.Link, Principal: principal(a.Principal),
		CreatedAt: a.CreatedAt.UTC(), UpdatedAt: a.UpdatedAt.UTC(), StartedAt: a.StartedAt.UTC(), Version: a.Version}
	if a.CompletedAt != nil {
		at := a.CompletedAt.UTC()
		out.CompletedAt = &at
	}
	return out
}

// branch is b as the API shows it: every branch but main with its base, status, checks and facts, any branch
// /cobranch.yaml names with what a merge into it needs, and any branch merged with its last merge.
func branch(b code.Branch) Branch {
	out := Branch{Name: b.Name, Head: b.Head, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt, Checks: mapAll(b.Checks, check)}
	if b.Commit != nil {
		c := commit(*b.Commit)
		out.Commit = &c
	}
	if b.Base != "" {
		status := BranchStatus(b.Status)
		out.Base, out.Status = &b.Base, &status
	}
	if b.Facts != nil {
		out.Ahead, out.Behind = &b.Facts.Ahead, &b.Facts.Behind
	}
	if r := b.Requirements; r != nil {
		out.Requirements = &MergeRequirements{Annotations: nonNil(r.Annotations), LinearHistory: r.LinearHistory}
	}
	if m := b.Merge; m != nil {
		out.Merge = &BranchMerge{Head: m.Head, By: principal(m.By), At: m.At.UTC()}
	}
	return out
}

// check is one of a branch's checks as the API shows it, each field only where its kind has it.
func check(c code.Check) BranchCheck {
	out := BranchCheck{Kind: BranchCheckKind(c.Kind), Met: c.Met}
	switch c.Kind {
	case code.CheckAnnotation:
		out.Name = &c.Name
		if c.Status != "" {
			status := AnnotationStatus(c.Status)
			out.Status = &status
		}
	default:
		out.Count = &c.Count
	}
	return out
}

// codeRepo finds the repository whose code the path names, by its organization's slug and its own, each current or
// old, for a viewer with repo.read on it. Unlike repo, it asks nothing of the organization, so a personal token that
// lists repo.read alone reads the code it clones.
func (a api) codeRepo(ctx context.Context, v repos.Viewer, orgSlug, slug string) (repos.Repository, error) {
	repo, _, err := a.repos.Locate(ctx, v, orgSlug, slug)
	if err != nil {
		return repos.Repository{}, fail(err, "repository")
	}
	return repo, nil
}

// ListBranches lists the repository's live branches.
func (a api) ListBranches(ctx context.Context, req ListBranchesRequestObject) (ListBranchesResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	list, err := a.code.Branches(ctx, v, repo.ID)
	if err != nil {
		return nil, fail(err, "repository")
	}
	return ListBranches200JSONResponse{Branches: mapAll(list, branch)}, nil
}

// GetBranch finds one of the repository's live branches.
func (a api) GetBranch(ctx context.Context, req GetBranchRequestObject) (GetBranchResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	b, err := a.code.Branch(ctx, v, repo.ID, req.BranchName)
	if err != nil {
		return nil, fail(err, "branch")
	}
	return GetBranch200JSONResponse(branch(b)), nil
}

// MergeBranch merges a branch into its base for the viewer.
func (a api) MergeBranch(ctx context.Context, req MergeBranchRequestObject) (MergeBranchResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	var in code.MergeInput
	if req.Body != nil {
		if req.Body.Head != nil {
			in.Head = *req.Body.Head
		}
		if req.Body.Method != nil {
			in.Method = code.MergeMethod(*req.Body.Method)
		}
	}
	merged, err := a.code.Merge(ctx, v, repo.ID, req.BranchName, in)
	if err != nil {
		return nil, mergeFail(err)
	}
	return MergeBranch200JSONResponse(MergeResult{Base: merged.Base, Head: merged.Head, Commit: merged.Commit}), nil
}

// mergeFail is fail for merges: 409 for a branch that moved and a merge Cobranch won't make, and 503 without a Git
// server.
func mergeFail(err error) error {
	var refused *code.MergeError
	switch {
	case errors.Is(err, code.ErrBranchMoved):
		return refuse(http.StatusConflict, "branch.moved", "The branch or its base moved since you read them: read them again.")
	case errors.As(err, &refused):
		return refuse(http.StatusConflict, refused.Code, refused.Message)
	case errors.Is(err, code.ErrUnavailable):
		return refuse(http.StatusServiceUnavailable, "git.unavailable", "This installation runs no Git server to merge with.")
	}
	return fail(err, "branch")
}

// ListCommits lists a page of a branch's first-parent history, and links the next.
func (a api) ListCommits(ctx context.Context, req ListCommitsRequestObject) (ListCommitsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	name, cursor, limit := git.DefaultBranch, "", 0
	if p := req.Params; p.Branch != nil {
		name = *p.Branch
	}
	if p := req.Params; p.Cursor != nil {
		cursor = *p.Cursor
	}
	if p := req.Params; p.Limit != nil {
		limit = *p.Limit
	}
	page, err := a.code.History(ctx, v, repo.ID, name, cursor, limit)
	if err != nil {
		return nil, fail(err, "branch")
	}
	out := ListCommits200JSONResponse{}
	out.Body.Commits = mapAll(page.Commits, commit)
	if page.Next != "" {
		next := url.Values{"branch": {name}, "cursor": {page.Next}}
		if limit != 0 {
			next.Set("limit", strconv.Itoa(limit))
		}
		link := `<?` + next.Encode() + `>; rel="next"`
		out.Headers.Link = &link
	}
	return out, nil
}

// GetCommit answers one of the repository's commits, with the push that first brought it.
func (a api) GetCommit(ctx context.Context, req GetCommitRequestObject) (GetCommitResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	c, p, err := a.code.Commit(ctx, v, repo.ID, req.SHA)
	if err != nil {
		return nil, fail(err, "commit")
	}
	out := commit(c)
	out.Push = &Push{Id: p.ID, Pusher: principal(p.Pusher), Transport: PushTransport(p.Transport), At: p.At.UTC()}
	return GetCommit200JSONResponse(out), nil
}

// ListAnnotations lists the annotations on one of the repository's commits.
func (a api) ListAnnotations(ctx context.Context, req ListAnnotationsRequestObject) (ListAnnotationsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	list, err := a.code.Annotations(ctx, v, repo.ID, req.SHA)
	if err != nil {
		return nil, fail(err, "commit")
	}
	return ListAnnotations200JSONResponse{Annotations: mapAll(list, annotation)}, nil
}

// SetAnnotation sets an annotation on one of the repository's commits for the viewer, or changes the viewer's own.
func (a api) SetAnnotation(ctx context.Context, req SetAnnotationRequestObject) (SetAnnotationResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	match, err := setPrecondition(req.Params.IfMatch, req.Params.IfNoneMatch)
	if err != nil {
		return nil, err
	}
	in := code.AnnotationInput{Name: req.Name, Status: code.AnnotationStatus(req.Body.Status), Version: match}
	if req.Body.Summary != nil {
		in.Summary = *req.Body.Summary
	}
	if req.Body.Link != nil {
		in.Link = *req.Body.Link
	}
	set, err := a.code.SetAnnotation(ctx, v, repo.ID, req.SHA, in)
	if err != nil {
		return nil, fail(err, "commit")
	}
	return SetAnnotation200JSONResponse{Body: annotation(set), Headers: SetAnnotation200ResponseHeaders{ETag: annotationTag(set)}}, nil
}

// DeleteAnnotation deletes an annotation from one of the repository's commits.
func (a api) DeleteAnnotation(ctx context.Context, req DeleteAnnotationRequestObject) (DeleteAnnotationResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	match, err := ifMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if match == 0 {
		return nil, refuse(http.StatusPreconditionRequired, "precondition.required", "Name the annotation's ETag in If-Match.")
	}
	if err := a.code.DeleteAnnotation(ctx, v, repo.ID, req.SHA, req.Name, match); err != nil {
		return nil, fail(err, "annotation")
	}
	return DeleteAnnotation204Response{}, nil
}

// annotationTag is a's ETag: its version, quoted.
func annotationTag(a code.Annotation) string {
	return `"` + strconv.FormatInt(a.Version, 10) + `"`
}

// setPrecondition is the version of an annotation that a PUT names: If-Match's, or code.NoAnnotation for If-None-Match
// *. It refuses with 428 a PUT that names neither, and with 400 one that names both or another If-None-Match.
func setPrecondition(ifMatchHeader, ifNoneMatch *string) (int64, error) {
	match, err := ifMatch(ifMatchHeader)
	none := ifNoneMatch != nil && strings.TrimSpace(*ifNoneMatch) != ""
	switch {
	case err != nil:
		return 0, err
	case none && match != 0:
		return 0, refuse(http.StatusBadRequest, "precondition.invalid", "Name If-Match to change the annotation or If-None-Match to set a new one, not both.")
	case none && strings.TrimSpace(*ifNoneMatch) != "*":
		return 0, refuse(http.StatusBadRequest, "precondition.invalid", "If-None-Match takes * alone: set the annotation only if it isn't there.")
	case none:
		return code.NoAnnotation, nil
	case match == 0:
		return 0, refuse(http.StatusPreconditionRequired, "precondition.required",
			"Name the annotation's ETag in If-Match to change it, or If-None-Match: * to set a new one.")
	}
	return match, nil
}

// ifMatch is the version of an annotation that If-Match names: 0 without the header, code.AnyVersion for *, and a
// refusal with 412 for a tag that names no version, a weak one included, since If-Match compares strongly.
func ifMatch(header *string) (int64, error) {
	if header == nil || strings.TrimSpace(*header) == "" {
		return 0, nil
	}
	tag := strings.TrimSpace(*header)
	if tag == "*" {
		return code.AnyVersion, nil
	}
	version, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(tag, `"`), `"`), 10, 64)
	if err != nil || version < 1 {
		return 0, refuse(http.StatusPreconditionFailed, "precondition.failed", "If-Match names no version of the annotation.")
	}
	return version, nil
}
