package graphql

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
)

// Code is what the API needs of the code's service.
type Code interface {
	Branches(ctx context.Context, v code.Viewer, repoID uuid.UUID) ([]code.Branch, error)
	History(ctx context.Context, v code.Viewer, repoID uuid.UUID, branch, cursor string, limit int) (code.Page, error)
	Commit(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha string) (code.Commit, code.Push, error)
	Tags(ctx context.Context, v code.Viewer, repoID uuid.UUID) ([]code.Tag, error)
	Tree(ctx context.Context, v code.Viewer, repoID uuid.UUID, ref, path string) (code.Tree, error)
	File(ctx context.Context, v code.Viewer, repoID uuid.UUID, ref, path string) (code.File, error)
}

// unavailable is the code of the error a field answers when the installation lacks what it needs: a Git server.
const unavailable = "UNAVAILABLE"

// misses are the reasons of the NOT_FOUND errors of what a ref or a path didn't find.
var misses = map[error]string{
	code.ErrRefNotFound:  "ref.not_found",
	code.ErrPathNotFound: "path.not_found",
	code.ErrNotTree:      "path.file",
	code.ErrNotBlob:      "path.directory",
	code.ErrSubmodule:    "path.submodule",
}

// codeFail is fail for the code's reads: NOT_FOUND with the reason of what a ref or a path didn't find, and
// UNAVAILABLE without a Git server.
func codeFail(ctx context.Context, err error, what string) error {
	for miss, reason := range misses {
		if errors.Is(err, miss) {
			return refuse(ctx, notFound, reason, miss.Error())
		}
	}
	if errors.Is(err, code.ErrUnavailable) {
		return refuse(ctx, unavailable, "git.unavailable", code.ErrUnavailable.Error())
	}
	return fail(ctx, err, "repository", what)
}

// entryKinds are the kinds of a tree's entries as the API shows them.
var entryKinds = map[code.EntryKind]EntryKind{
	code.KindTree: EntryKindTree, code.KindBlob: EntryKindBlob, code.KindSymlink: EntryKindSymlink, code.KindSubmodule: EntryKindSubmodule,
}

func entry(e code.Entry) *TreeEntry {
	out := &TreeEntry{Name: e.Name, Path: e.Path, Kind: entryKinds[e.Kind], Mode: e.Mode, Sha: e.SHA}
	if e.Kind == code.KindBlob || e.Kind == code.KindSymlink {
		size := int(e.Size)
		out.Size = &size
	}
	return out
}

func file(f code.File) *File {
	out := &File{Commit: f.Commit, Path: f.Path, Name: f.Name, Kind: entryKinds[f.Kind], Mode: f.Mode, Sha: f.SHA, Size: int(f.Size),
		Binary: f.Binary, Truncated: f.Truncated}
	if !f.Binary {
		out.Text = &f.Text
	}
	if f.HTML != "" {
		out.HTML = &f.HTML
	}
	return out
}

func commitPrincipal(p *code.Principal) *CommitPrincipal {
	if p == nil {
		return nil
	}
	return &CommitPrincipal{ID: p.ID.String(), DisplayName: p.DisplayName}
}

func person(p code.Person) *Person {
	return &Person{Name: p.Name, Email: p.Email, At: p.At.UTC(), Zone: p.Zone, Principal: commitPrincipal(p.Principal)}
}

// commit is c as the API shows it; a list's commits come without their pushes.
func commit(c code.Commit) *Commit {
	parents := c.Parents
	if parents == nil {
		parents = []string{}
	}
	return &Commit{Sha: c.SHA, Tree: c.Tree, Parents: parents, Message: c.Message, Author: person(c.Author), Committer: person(c.Committer),
		Signature: c.Signature}
}

func branch(b code.Branch) *Branch {
	out := &Branch{Name: b.Name, Head: b.Head, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt}
	if b.Commit != nil {
		out.Commit = commit(*b.Commit)
	}
	return out
}
