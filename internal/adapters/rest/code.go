package rest

import (
	"context"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

// Code is what the API needs of the code's service.
type Code interface {
	Branches(ctx context.Context, v code.Viewer, repoID uuid.UUID) ([]code.Branch, error)
	History(ctx context.Context, v code.Viewer, repoID uuid.UUID, branch, cursor string, limit int) (code.Page, error)
	Commit(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha string) (code.Commit, code.Push, error)
	Tags(ctx context.Context, v code.Viewer, repoID uuid.UUID) ([]code.Tag, error)
	Tree(ctx context.Context, v code.Viewer, repoID uuid.UUID, ref, path string) (code.Tree, error)
	File(ctx context.Context, v code.Viewer, repoID uuid.UUID, ref, path string) (code.File, error)
	Raw(ctx context.Context, v code.Viewer, repoID uuid.UUID, ref, path string) (code.Raw, error)
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

// commit is c as the API shows it; a list's commits come without their pushes.
func commit(c code.Commit) Commit {
	return Commit{Sha: c.SHA, Tree: c.Tree, Parents: nonNil(c.Parents), Message: c.Message, Author: person(c.Author),
		Committer: person(c.Committer), Signature: c.Signature}
}

func branch(b code.Branch) Branch {
	out := Branch{Name: b.Name, Head: b.Head, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt}
	if b.Commit != nil {
		c := commit(*b.Commit)
		out.Commit = &c
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
