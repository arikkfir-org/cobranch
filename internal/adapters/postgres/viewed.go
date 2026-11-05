package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// ViewedFiles lists actor's Viewed marks on the repository's live branch of name, by path: repos.ErrNotFound when it
// has no such branch (code.ViewedStore).
func (s *CodeStore) ViewedFiles(ctx context.Context, actor, repoID uuid.UUID, branch string) ([]code.ViewedFile, error) {
	b, err := viewedBranch(ctx, s.q, repoID, branch)
	if err != nil {
		return nil, err
	}
	return viewedFiles(ctx, s.q, actor, b.ID)
}

// UpdateViewed applies changes, at at, to actor's Viewed marks on the repository's live branch of name, as actor,
// once check passes on the marks as they are; the marks stay locked from that read to the transaction's end, so
// another change to them waits (code.ViewedStore).
func (s *CodeStore) UpdateViewed(ctx context.Context, actor, repoID uuid.UUID, branch string, check func([]code.ViewedFile) error,
	changes []code.ViewedChange, at time.Time) ([]code.ViewedFile, error) {
	var marks []code.ViewedFile
	err := transact(ctx, s.pool, s.q, "changing the Viewed marks", actor, func(ctx context.Context, q *queries.Queries) error {
		b, err := viewedBranch(ctx, q, repoID, branch)
		if err != nil {
			return err
		}
		if err := q.LockViewed(ctx, actor.String()+"/"+b.ID.String()); err != nil {
			return fmt.Errorf("locking them: %w", err)
		}
		before, err := viewedFiles(ctx, q, actor, b.ID)
		if err != nil {
			return err
		}
		if err := check(before); err != nil {
			return err
		}
		if err := applyViewed(ctx, q, b, actor, changes, at); err != nil {
			return err
		}
		marks, err = viewedFiles(ctx, q, actor, b.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return marks, nil
}

// applyViewed applies changes, at at, to actor's Viewed marks on branch b, and refuses marks past the
// code.MaxViewedFiles a branch keeps.
func applyViewed(ctx context.Context, q *queries.Queries, b queries.ViewedBranchRow, actor uuid.UUID, changes []code.ViewedChange, at time.Time) error {
	for _, c := range changes {
		var err error
		if c.Unmark {
			err = q.UnmarkViewed(ctx, actor, b.ID, c.Path)
		} else {
			err = q.MarkViewed(ctx, queries.MarkViewedParams{OrgID: b.OrgID, PrincipalID: actor, BranchID: b.ID, Path: c.Path, Object: c.Object, ViewedAt: at})
		}
		if err != nil {
			return fmt.Errorf("writing the mark on %q: %w", c.Path, err)
		}
	}
	n, err := q.CountViewed(ctx, actor, b.ID)
	if err != nil {
		return fmt.Errorf("counting them: %w", err)
	}
	if n > code.MaxViewedFiles {
		return &orgs.ValidationError{Field: "files", Code: "viewed.too_many",
			Message: fmt.Sprintf("at most %d files of a branch marked Viewed: take the marks off files that left its change", code.MaxViewedFiles)}
	}
	return nil
}

// viewedBranch finds the IDs of the repository's live branch of name and of its organization, or repos.ErrNotFound.
func viewedBranch(ctx context.Context, q *queries.Queries, repoID uuid.UUID, name string) (queries.ViewedBranchRow, error) {
	b, err := q.ViewedBranch(ctx, repoID, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, repos.ErrNotFound
	}
	if err != nil {
		return b, fmt.Errorf("finding the branch %q: %w", name, err)
	}
	return b, nil
}

// viewedFiles lists actor's Viewed marks on the branch of id, by path.
func viewedFiles(ctx context.Context, q *queries.Queries, actor, branchID uuid.UUID) ([]code.ViewedFile, error) {
	rows, err := q.ViewedFiles(ctx, actor, branchID)
	if err != nil {
		return nil, fmt.Errorf("reading the Viewed marks: %w", err)
	}
	marks := make([]code.ViewedFile, 0, len(rows))
	for _, r := range rows {
		marks = append(marks, code.ViewedFile{Path: r.Path, Object: r.Object, ViewedAt: r.ViewedAt})
	}
	return marks, nil
}
