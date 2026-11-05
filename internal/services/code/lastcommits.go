package code

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
)

// TreeCommits is what last changed a directory at a commit, as people read it: the commit its ref resolved to, the
// directory's path, how many commits changed it, the latest of them, and the latest that changed each of its entries,
// by name, each naming one of Commits, which holds every such commit once, newest first. A commit no push recorded is
// left out, and so is an entry nobody changed in the MaxLastWalk commits the walk reads.
type TreeCommits struct {
	Commit  string
	Path    string
	Count   int
	Latest  string
	Entries map[string]string
	Commits []Commit
}

// TreeCommits reads what last changed the directory at path in the commit ref names (a full commit name, a branch or a
// tag, in that order), for a listing of its entries to show beside each; it takes repo.read.
func (s *Service) TreeCommits(ctx context.Context, v Viewer, repoID uuid.UUID, ref, path string) (TreeCommits, error) {
	ctx, span := tracer.Start(ctx, "code.Service.TreeCommits")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.ref", ref), attribute.String("cobranch.path", path))
	if err := s.check(ctx, v, repoID, ref, path); err != nil {
		return TreeCommits{}, failed(span, "reading the tree's last commits", err)
	}
	last, err := s.objects.LastCommits(ctx, repoID, ref, path)
	if err != nil {
		return TreeCommits{}, failed(span, "reading the tree's last commits", err)
	}
	out, err := s.recordedLast(ctx, repoID, last)
	if err == nil {
		err = s.renderCommits(ctx, repoID, out.Commits)
	}
	if err != nil {
		return TreeCommits{}, failed(span, "reading the tree's last commits", err)
	}
	return out, nil
}

// recordedLast is last with the commits pushes recorded, each once, newest first by when it was committed, and
// without the names of any they didn't.
func (s *Service) recordedLast(ctx context.Context, repoID uuid.UUID, last LastCommits) (TreeCommits, error) {
	shas := make([]string, 0, len(last.Entries)+1)
	if last.Latest != "" {
		shas = append(shas, last.Latest)
	}
	for _, sha := range last.Entries {
		if !slices.Contains(shas, sha) {
			shas = append(shas, sha)
		}
	}
	recorded, err := s.store.CommitsAt(ctx, repoID, shas)
	if err != nil {
		return TreeCommits{}, fmt.Errorf("reading the commits that last changed %q: %w", last.Path, err)
	}
	out := TreeCommits{Commit: last.Commit, Path: last.Path, Count: last.Count, Entries: map[string]string{}}
	if _, ok := recorded[last.Latest]; ok {
		out.Latest = last.Latest
	}
	for name, sha := range last.Entries {
		if _, ok := recorded[sha]; ok {
			out.Entries[name] = sha
		}
	}
	out.Commits = newestFirst(recorded)
	return out, nil
}

// newestFirst is commits, the most recently committed first, and by name among those committed at once.
func newestFirst(commits map[string]Commit) []Commit {
	out := slices.SortedFunc(maps.Values(commits), func(a, b Commit) int {
		return cmp.Or(b.Committer.At.Compare(a.Committer.At), cmp.Compare(a.SHA, b.SHA))
	})
	if out == nil {
		out = []Commit{}
	}
	return out
}
