package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/git"
)

// Crowded lists the repositories that have at least threshold active packs (git.PackStore).
func (s *GitStore) Crowded(ctx context.Context, threshold int) ([]uuid.UUID, error) {
	ids, err := s.q.CrowdedRepositories(ctx, int64(threshold))
	if err != nil {
		return nil, fmt.Errorf("finding repositories of %d active packs or more: %w", threshold, err)
	}
	return ids, nil
}

// Compact lists the compacted pack and supersedes the packs it replaces in one transaction that locks the repository,
// so it applies between two pushes, never beside one (git.PackStore). Cobranch's own change, it names nobody.
func (s *GitStore) Compact(ctx context.Context, repoID uuid.UUID, pack git.Pack, superseded []string) error {
	return transact(ctx, s.pool, s.q, "listing a compacted pack", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		orgID, err := q.LockRepository(ctx, repoID)
		if errors.Is(err, pgx.ErrNoRows) {
			return git.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("locking the repository: %w", err)
		}
		if err := q.InsertPack(ctx, queries.InsertPackParams{RepoID: repoID, Name: pack.Name, OrgID: orgID, Upload: pack.Upload,
			Size: pack.Size, Objects: pack.Objects}); err != nil {
			return fmt.Errorf("listing the pack: %w", err)
		}
		n, err := q.SupersedePacks(ctx, repoID, superseded)
		if err != nil {
			return fmt.Errorf("superseding the packs it replaces: %w", err)
		}
		if n != int64(len(superseded)) {
			return fmt.Errorf("%d of the %d packs it replaces are active: %w", n, len(superseded), git.ErrPacksChanged)
		}
		return nil
	})
}

// Superseded lists the packs superseded before the time (git.PackStore).
func (s *GitStore) Superseded(ctx context.Context, before time.Time) ([]git.Superseded, error) {
	rows, err := s.q.SupersededPacks(ctx, before)
	if err != nil {
		return nil, fmt.Errorf("listing packs superseded before %s: %w", before, err)
	}
	packs := make([]git.Superseded, 0, len(rows))
	for _, r := range rows {
		packs = append(packs, git.Superseded{RepoID: r.RepoID, Pack: git.Pack{Name: r.Name, Upload: r.Upload}})
	}
	return packs, nil
}

// Forget deletes a superseded pack's row, unless a push made it active again (git.PackStore).
func (s *GitStore) Forget(ctx context.Context, p git.Superseded) error {
	if err := s.q.ForgetPack(ctx, p.RepoID, p.Pack.Name, p.Pack.Upload); err != nil {
		return fmt.Errorf("forgetting pack %s of repository %s: %w", p.Pack.Name, p.RepoID, err)
	}
	return nil
}

// Uploads lists every upload a pack's row names (git.PackStore).
func (s *GitStore) Uploads(ctx context.Context) ([]uuid.UUID, error) {
	ids, err := s.q.PackUploads(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing the packs' uploads: %w", err)
	}
	return ids, nil
}
