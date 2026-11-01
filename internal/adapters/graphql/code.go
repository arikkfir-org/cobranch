package graphql

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/orgs"
)

// Code is what the API needs of the code's service.
type Code interface {
	Branches(ctx context.Context, v code.Viewer, repoID uuid.UUID) ([]code.Branch, error)
	Branch(ctx context.Context, v code.Viewer, repoID uuid.UUID, name string) (code.Branch, error)
	History(ctx context.Context, v code.Viewer, repoID uuid.UUID, branch, cursor string, limit int) (code.Page, error)
	Commit(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha string) (code.Commit, code.Push, error)
	SetAnnotation(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha string, in code.AnnotationInput) (code.Annotation, error)
	DeleteAnnotation(ctx context.Context, v code.Viewer, repoID uuid.UUID, sha, name string, version int64) error
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
		Signature: c.Signature, Annotations: mapAll(c.Annotations, annotation)}
}

// annotationStatuses maps the domain's annotation statuses to the schema's, and back.
var annotationStatuses = map[code.AnnotationStatus]AnnotationStatus{
	code.AnnotationPending: AnnotationStatusPending, code.AnnotationInProgress: AnnotationStatusInProgress,
	code.AnnotationFailed: AnnotationStatusFailed, code.AnnotationSuccessful: AnnotationStatusSuccessful,
}

func annotation(a code.Annotation) *Annotation {
	out := &Annotation{ID: a.ID.String(), Name: a.Name, Status: annotationStatuses[a.Status], Summary: a.Summary, Link: a.Link,
		Principal: commitPrincipal(a.Principal), CreatedAt: a.CreatedAt.UTC(), UpdatedAt: a.UpdatedAt.UTC(), StartedAt: a.StartedAt.UTC(),
		Version: int(a.Version)}
	if a.CompletedAt != nil {
		at := a.CompletedAt.UTC()
		out.CompletedAt = &at
	}
	return out
}

// annotationInput is in as the domain takes it, for the annotation of name.
func annotationInput(name string, in AnnotationInput) code.AnnotationInput {
	out := code.AnnotationInput{Name: name, Summary: in.Summary, Link: in.Link}
	for domain, schema := range annotationStatuses {
		if schema == in.Status {
			out.Status = domain
		}
	}
	return out
}

// expected is the version of an annotation that expectedVersion names, from 1: 0 without one.
func expected(expectedVersion *int) (int64, error) {
	if expectedVersion == nil {
		return 0, nil
	}
	if *expectedVersion < 1 {
		return 0, &orgs.ValidationError{Field: "expectedVersion", Code: "version.invalid", Message: "a version of the annotation, from 1"}
	}
	return int64(*expectedVersion), nil
}

// codeRepo is the viewer and the repository a mutation of its code names.
func codeRepo(ctx context.Context, repositoryID string) (code.Viewer, uuid.UUID, error) {
	v, err := viewer(ctx)
	if err != nil {
		return code.Viewer{}, uuid.Nil, err
	}
	repoID, err := parseID(ctx, repositoryID, "repository")
	if err != nil {
		return code.Viewer{}, uuid.Nil, err
	}
	return v.User, repoID, nil
}

// branch is b as the API shows it: every branch but main with its base, status, checks and facts, any branch
// /cobranch.yaml names with what a merge into it needs, and any branch merged with its last merge.
func branch(b code.Branch) *Branch {
	out := &Branch{Name: b.Name, Head: b.Head, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt, Checks: mapAll(b.Checks, check)}
	if b.Commit != nil {
		out.Commit = commit(*b.Commit)
	}
	if b.Base != "" {
		status := branchStatuses[b.Status]
		out.Base, out.Status = &b.Base, &status
	}
	if b.Facts != nil {
		out.Ahead, out.Behind = &b.Facts.Ahead, &b.Facts.Behind
	}
	if r := b.Requirements; r != nil {
		annotations := r.Annotations
		if annotations == nil {
			annotations = []string{}
		}
		out.Requirements = &MergeRequirements{Annotations: annotations, LinearHistory: r.LinearHistory}
	}
	if m := b.Merge; m != nil {
		out.Merge = &BranchMerge{Head: m.Head, By: commitPrincipal(m.By), At: m.At.UTC()}
	}
	return out
}

// branchStatuses maps the domain's branch statuses to the schema's.
var branchStatuses = map[code.BranchStatus]BranchStatus{
	code.StatusProcessing: BranchStatusProcessing, code.StatusBlocked: BranchStatusBlocked, code.StatusEmpty: BranchStatusEmpty,
	code.StatusMerged: BranchStatusMerged, code.StatusReady: BranchStatusReady,
}

// checkKinds maps the domain's kinds of checks to the schema's.
var checkKinds = map[code.CheckKind]BranchCheckKind{
	code.CheckAnnotation: BranchCheckKindAnnotation, code.CheckLinearHistory: BranchCheckKindLinearHistory, code.CheckUpToDate: BranchCheckKindUpToDate,
}

// check is one of a branch's checks as the API shows it, each field only where its kind has it.
func check(c code.Check) *BranchCheck {
	out := &BranchCheck{Kind: checkKinds[c.Kind], Met: c.Met}
	switch c.Kind {
	case code.CheckAnnotation:
		out.Name = &c.Name
		if c.Status != "" {
			status := annotationStatuses[c.Status]
			out.Status = &status
		}
	default:
		out.Count = &c.Count
	}
	return out
}
