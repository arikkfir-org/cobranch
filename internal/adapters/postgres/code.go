package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

// CodeStore reads what pushes recorded of repositories' code (db/migrations/00012_git.sql): code.Store on PostgreSQL,
// through the queries sqlc generates from db/queries.
type CodeStore struct {
	q *queries.Queries
}

// NewCodeStore returns the code's store on pool's database.
func NewCodeStore(pool *pgxpool.Pool) *CodeStore {
	return &CodeStore{q: queries.New(pool)}
}

var _ code.Store = (*CodeStore)(nil)

// Branches lists the repository's live branches, each with its head commit when a push recorded it (code.Store).
func (s *CodeStore) Branches(ctx context.Context, repoID uuid.UUID) ([]code.Branch, error) {
	rows, err := s.q.LiveBranches(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("listing the branches: %w", err)
	}
	out := make([]code.Branch, 0, len(rows))
	heads := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, code.Branch{Name: r.Name, Head: r.Head, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
		heads = append(heads, r.Head)
	}
	commits, err := s.commitsAt(ctx, repoID, heads)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if c, ok := commits[out[i].Head]; ok {
			out[i].Commit = &c
		}
	}
	return out, nil
}

// Branch finds one of the repository's live branches, with its head commit, or repos.ErrNotFound (code.Store).
func (s *CodeStore) Branch(ctx context.Context, repoID uuid.UUID, name string) (code.Branch, error) {
	r, err := s.q.LiveBranch(ctx, repoID, name)
	if err != nil {
		return code.Branch{}, wrapRepo("finding the branch", err)
	}
	b := code.Branch{Name: r.Name, Head: r.Head, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	commits, err := s.commitsAt(ctx, repoID, []string{r.Head})
	if err != nil {
		return code.Branch{}, err
	}
	if c, ok := commits[r.Head]; ok {
		b.Commit = &c
	}
	return b, nil
}

// Tags lists the repository's tags by name, each with the object its ref names (code.Store).
func (s *CodeStore) Tags(ctx context.Context, repoID uuid.UUID) ([]code.Tag, error) {
	rows, err := s.q.Tags(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("listing the tags: %w", err)
	}
	out := make([]code.Tag, 0, len(rows))
	for _, r := range rows {
		out = append(out, code.Tag{Name: r.Name, Target: r.Sha})
	}
	return out, nil
}

// Slugs finds the repository's slug and its organization's (code.Store).
func (s *CodeStore) Slugs(ctx context.Context, repoID uuid.UUID) (org, repo string, err error) {
	row, err := s.q.Slugs(ctx, repoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", repos.ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("reading the repository's slugs: %w", err)
	}
	return row.Org, row.Repo, nil
}

// commitsAt finds those of the repository's commits that shas name, by name.
func (s *CodeStore) commitsAt(ctx context.Context, repoID uuid.UUID, shas []string) (map[string]code.Commit, error) {
	if len(shas) == 0 {
		return nil, nil
	}
	rows, err := s.q.CommitsAt(ctx, repoID, shas)
	if err != nil {
		return nil, fmt.Errorf("reading the commits: %w", err)
	}
	out := make(map[string]code.Commit, len(rows))
	for _, r := range rows {
		out[r.Commit.Sha] = commitOf(r.Commit, r.AuthorDisplayName, r.CommitterDisplayName)
	}
	return out, nil
}

// FirstParents lists at most limit of the repository's commits from sha along first parents, nearest first
// (code.Store).
func (s *CodeStore) FirstParents(ctx context.Context, repoID uuid.UUID, sha string, limit int) ([]code.Commit, error) {
	rows, err := s.q.FirstParents(ctx, repoID, sha, int32(min(limit, code.MaxPageSize+1)))
	if err != nil {
		return nil, fmt.Errorf("walking the first parents of %s: %w", sha, err)
	}
	out := make([]code.Commit, 0, len(rows))
	for _, r := range rows {
		out = append(out, commitOf(r.Commit, r.AuthorDisplayName, r.CommitterDisplayName))
	}
	return out, nil
}

// Commit finds one of the repository's commits with the push that first brought it, or repos.ErrNotFound
// (code.Store).
func (s *CodeStore) Commit(ctx context.Context, repoID uuid.UUID, sha string) (code.Commit, code.Push, error) {
	commits, err := s.commitsAt(ctx, repoID, []string{sha})
	if err != nil {
		return code.Commit{}, code.Push{}, err
	}
	c, ok := commits[sha]
	if !ok {
		return code.Commit{}, code.Push{}, repos.ErrNotFound
	}
	p, err := s.q.PushOf(ctx, repoID, c.FirstPush)
	if errors.Is(err, pgx.ErrNoRows) {
		return code.Commit{}, code.Push{}, fmt.Errorf("finding push %s, which brought commit %s: none", c.FirstPush, sha)
	}
	if err != nil {
		return code.Commit{}, code.Push{}, fmt.Errorf("finding push %s, which brought commit %s: %w", c.FirstPush, sha, err)
	}
	return c, code.Push{ID: p.ID, Pusher: principalOf(p.PusherID, p.PusherDisplayName), Transport: git.Transport(p.Transport), At: p.CommittedAt}, nil
}

// commitOf is the domain's view of a commits row, with the display names of the principals behind its author and
// committer.
func commitOf(c queries.Commit, author, committer *string) code.Commit {
	return code.Commit{SHA: c.Sha, Tree: c.Tree, Parents: c.Parents, Message: c.Message, Signature: c.Signature, FirstPush: c.FirstPush,
		Author:    code.Person{Name: c.AuthorName, Email: c.AuthorEmail, At: c.AuthoredAt, Zone: c.AuthorZone, Principal: principalOf(c.AuthorID, author)},
		Committer: code.Person{Name: c.CommitterName, Email: c.CommitterEmail, At: c.CommittedAt, Zone: c.CommitterZone, Principal: principalOf(c.CommitterID, committer)}}
}

// principalOf is the principal id names, with its display name, or nil without one.
func principalOf(id *uuid.UUID, displayName *string) *code.Principal {
	if id == nil || displayName == nil {
		return nil
	}
	return &code.Principal{ID: *id, DisplayName: *displayName}
}
