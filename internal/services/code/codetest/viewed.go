package codetest

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// viewedKey names a principal's Viewed marks on a repository's branch.
func viewedKey(actor, repoID uuid.UUID, branch string) string {
	return actor.String() + "/" + repoID.String() + "/" + branch
}

// ViewedFiles lists actor's Viewed marks on the repository's live branch of name, by path, or repos.ErrNotFound.
func (s *Store) ViewedFiles(ctx context.Context, actor, repoID uuid.UUID, branch string) ([]code.ViewedFile, error) {
	if s.Fail != nil {
		return nil, s.Fail
	}
	if _, err := s.Branch(ctx, repoID, branch); err != nil {
		return nil, err
	}
	return slices.Clone(s.Viewed[viewedKey(actor, repoID, branch)]), nil
}

// UpdateViewed applies changes, at at, to actor's Viewed marks on the repository's live branch of name once check
// passes on them, keeping a mark already at its object as it was, and answers them by path.
func (s *Store) UpdateViewed(ctx context.Context, actor, repoID uuid.UUID, branch string, check func([]code.ViewedFile) error,
	changes []code.ViewedChange, at time.Time) ([]code.ViewedFile, error) {
	if s.Fail != nil {
		return nil, s.Fail
	}
	if _, err := s.Branch(ctx, repoID, branch); err != nil {
		return nil, repos.ErrNotFound
	}
	key := viewedKey(actor, repoID, branch)
	marks := slices.Clone(s.Viewed[key])
	if err := check(marks); err != nil {
		return nil, err
	}
	for _, c := range changes {
		i := slices.IndexFunc(marks, func(m code.ViewedFile) bool { return m.Path == c.Path })
		switch {
		case c.Unmark && i >= 0:
			marks = slices.Delete(marks, i, i+1)
		case c.Unmark:
		case i < 0:
			marks = append(marks, code.ViewedFile{Path: c.Path, Object: c.Object, ViewedAt: at})
		case marks[i].Object != c.Object:
			marks[i] = code.ViewedFile{Path: c.Path, Object: c.Object, ViewedAt: at}
		}
	}
	if len(marks) > code.MaxViewedFiles {
		return nil, &orgs.ValidationError{Field: "files", Code: "viewed.too_many", Message: "too many files marked Viewed"}
	}
	slices.SortFunc(marks, func(a, b code.ViewedFile) int { return strings.Compare(a.Path, b.Path) })
	s.Viewed[key] = marks
	return slices.Clone(marks), nil
}
