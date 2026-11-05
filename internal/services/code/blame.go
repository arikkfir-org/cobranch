package code

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/orgs"
)

// FileBlame is which commit last changed each line of a file at a commit, as people read it: the commit its ref
// resolved to, the file's path, its lines in runs, in order, each the lines one commit last changed, and Commits, each
// commit the runs name that a push recorded, once, newest first. A run whose commit no push recorded keeps its name,
// with no commit of Commits to show.
type FileBlame struct {
	Commit  string
	Path    string
	Ranges  []BlameRange
	Commits []Commit
}

// Blame reads which commit last changed each line of the file at path in the commit ref names (a full commit name, a
// branch or a tag, in that order), as git blame tells it, for the file's page to show beside its lines; it takes
// repo.read. Only a file whose text the API shows has one: a symlink, a binary file or one over MaxText is a path.text
// ValidationError.
func (s *Service) Blame(ctx context.Context, v Viewer, repoID uuid.UUID, ref, path string) (FileBlame, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Blame")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.ref", ref), attribute.String("cobranch.path", path))
	if err := s.check(ctx, v, repoID, ref, path); err != nil {
		return FileBlame{}, failed(span, "reading the file's blame", err)
	}
	if path == "" {
		return FileBlame{}, ErrNotBlob
	}
	// The file's first bytes tell whether it has text to blame; its commit pins the blame to the same file.
	blob, err := s.objects.Blob(ctx, repoID, ref, path, binaryProbe)
	if err != nil {
		return FileBlame{}, failed(span, "reading the file's blame", err)
	}
	if blob.Kind != KindBlob || blob.Size > MaxText || Binary(blob.Content, blob.Truncated) {
		return FileBlame{}, &orgs.ValidationError{Field: "path", Code: "path.text",
			Message: "a file whose text the API shows: not a symlink, not binary, and at most 1 MiB"}
	}
	blame, err := s.objects.Blame(ctx, repoID, blob.Commit, path)
	if err != nil {
		return FileBlame{}, failed(span, "reading the file's blame", err)
	}
	out, err := s.recordedBlame(ctx, repoID, blame)
	if err == nil {
		err = s.renderCommits(ctx, repoID, out.Commits)
	}
	if err != nil {
		return FileBlame{}, failed(span, "reading the file's blame", err)
	}
	return out, nil
}

// recordedBlame is blame with the commits pushes recorded of those its runs name, each once, newest first.
func (s *Service) recordedBlame(ctx context.Context, repoID uuid.UUID, blame Blame) (FileBlame, error) {
	seen := map[string]bool{}
	shas := []string{}
	for _, r := range blame.Ranges {
		if !seen[r.SHA] {
			seen[r.SHA] = true
			shas = append(shas, r.SHA)
		}
	}
	recorded, err := s.store.CommitsAt(ctx, repoID, shas)
	if err != nil {
		return FileBlame{}, fmt.Errorf("reading the commits that last changed %q's lines: %w", blame.Path, err)
	}
	ranges := blame.Ranges
	if ranges == nil {
		ranges = []BlameRange{}
	}
	return FileBlame{Commit: blame.Commit, Path: blame.Path, Ranges: ranges, Commits: newestFirst(recorded)}, nil
}
