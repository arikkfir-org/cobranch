package code

import (
	"context"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/orgs"
)

// The limits of a diff (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/reviews.md, "Diffs"):
// past them, a diff says what it left out rather than grow without end.
const (
	// MaxDiffFiles is the most files a diff lists; it counts the others in FilesLeftOut.
	MaxDiffFiles = 3000
	// MaxDiffLines is the most lines of hunks a diff holds in all; the files past it are listed without their hunks.
	MaxDiffLines = 20000
	// MaxFileDiffLines is the most lines of hunks one file's diff holds; a file past it is listed without its hunks.
	MaxFileDiffLines = 5000
	// MaxDiffLineBytes is the longest line a hunk holds; a file with a longer line is listed without its hunks.
	MaxDiffLineBytes = 64 << 10
)

// FileStatus is what a diff did to a file.
type FileStatus string

// The statuses of a file in a diff.
const (
	FileAdded    FileStatus = "added"
	FileModified FileStatus = "modified"
	FileDeleted  FileStatus = "deleted"
	FileRenamed  FileStatus = "renamed"
	// FileTypeChanged is a path whose kind changed, such as a file that became a symlink.
	FileTypeChanged FileStatus = "type_changed"
)

// LineKind is what a line of a hunk is.
type LineKind string

// The kinds of a hunk's lines.
const (
	LineContext LineKind = "context"
	LineAdded   LineKind = "added"
	LineRemoved LineKind = "removed"
)

// DiffLine is one line of a hunk, without its newline: its number in the old file, 0 for an added line, and in the
// new, 0 for a removed one. NoNewline reports that the file ends on it without one.
type DiffLine struct {
	Kind      LineKind
	Old       int
	New       int
	Text      string
	NoNewline bool
}

// Hunk is one run of a file's changes with the lines around them: where it starts in the old file and the new, how
// many lines it spans in each, and the section heading Git found above it, such as a function's.
type Hunk struct {
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Section  string
	Lines    []DiffLine
}

// FileDiff is one file's change: its path, and the one it had before when it was renamed; its modes, "" where it is
// absent; how many lines it adds and removes, 0 for a binary file; and its hunks, none for a binary file and when
// Truncated, when it passes MaxFileDiffLines or MaxDiffLineBytes, or the diff passed MaxDiffLines before it.
type FileDiff struct {
	Status    FileStatus
	Path      string
	OldPath   string
	OldMode   string
	Mode      string
	Binary    bool
	Additions int
	Deletions int
	Truncated bool
	Hunks     []Hunk
}

// Diff is the change from one commit to another: Base, "" for the empty tree a root commit starts from, and Head;
// for a branch, Base is the merge base of its base's tip and its head, and BaseTip that tip. It lists its files in
// Git's order, at most MaxDiffFiles, and counts those it left out.
type Diff struct {
	Base         string
	BaseTip      string
	Head         string
	Files        []FileDiff
	FilesLeftOut int
	Additions    int
	Deletions    int
}

// DiffRequest is the diff the Git server is asked for: from the commit Base names, or the empty tree when it is "",
// or, when MergeBase, from the merge base of that commit and Head's; to the commit Head names.
type DiffRequest struct {
	Base      string
	Head      string
	MergeBase bool
}

// BranchDiff is a branch's change: from the merge base of its base's tip and its head to its head, so what its base
// gained since doesn't show; it takes repo.read. The default branch has no base, and so no diff.
func (s *Service) BranchDiff(ctx context.Context, v Viewer, repoID uuid.UUID, branch string) (Diff, error) {
	ctx, span := tracer.Start(ctx, "code.Service.BranchDiff")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", branch))
	if err := s.read(ctx, v, repoID); err != nil {
		return Diff{}, failed(span, "reading the branch's diff", err)
	}
	b, err := s.store.Branch(ctx, repoID, branch)
	if err != nil {
		return Diff{}, failed(span, "reading the branch's diff", err)
	}
	if b.Base == "" {
		return Diff{}, &orgs.ValidationError{Field: "branch", Code: "branch.default",
			Message: "the default branch merges into nothing, so it has no diff"}
	}
	d, err := s.objects.Diff(ctx, repoID, DiffRequest{Base: b.Base, Head: b.Head, MergeBase: true})
	if err != nil {
		return Diff{}, failed(span, "reading the branch's diff", err)
	}
	return d, nil
}

// CommitDiff is a commit's change against its first parent, or against the empty tree when it has none; it takes
// repo.read.
func (s *Service) CommitDiff(ctx context.Context, v Viewer, repoID uuid.UUID, sha string) (Diff, error) {
	ctx, span := tracer.Start(ctx, "code.Service.CommitDiff")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.commit.sha", sha))
	if err := s.read(ctx, v, repoID); err != nil {
		return Diff{}, failed(span, "reading the commit's diff", err)
	}
	if err := ValidateSHA("sha", sha); err != nil {
		return Diff{}, err
	}
	c, _, err := s.store.Commit(ctx, repoID, sha)
	if err != nil {
		return Diff{}, failed(span, "reading the commit's diff", err)
	}
	base := ""
	if len(c.Parents) > 0 {
		base = c.Parents[0]
	}
	d, err := s.objects.Diff(ctx, repoID, DiffRequest{Base: base, Head: c.SHA})
	if err != nil {
		return Diff{}, failed(span, "reading the commit's diff", err)
	}
	return d, nil
}
