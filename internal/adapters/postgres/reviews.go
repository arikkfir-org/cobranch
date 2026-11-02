package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/repos"
)

// Reviews lists the reviews on one of the repository's commits in the order they were submitted, or repos.ErrNotFound
// when it holds no such commit (code.Store).
func (s *CodeStore) Reviews(ctx context.Context, repoID uuid.UUID, sha string) ([]code.Review, error) {
	if _, err := s.q.CommitOrg(ctx, repoID, sha); errors.Is(err, pgx.ErrNoRows) {
		return nil, repos.ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("finding commit %s: %w", sha, err)
	}
	byCommit, err := reviewsAt(ctx, s.q, repoID, []string{sha})
	if err != nil {
		return nil, err
	}
	return byCommit[sha], nil
}

// Review finds one of the repository's reviews, or repos.ErrNotFound (code.Store).
func (s *CodeStore) Review(ctx context.Context, repoID, id uuid.UUID) (code.Review, error) {
	row, err := s.q.ReviewOf(ctx, repoID, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return code.Review{}, repos.ErrNotFound
	}
	if err != nil {
		return code.Review{}, fmt.Errorf("finding review %s: %w", id, err)
	}
	return reviewOf(row.Review, row.ReviewerDisplayName, row.ReviewerKind), nil
}

// SubmitReview records actor's review of the repository's commit in one transaction that acts for actor: it appends
// event review.submitted and works out again the status of the branches at the commit (code.Store).
func (s *CodeStore) SubmitReview(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, sha string, in code.ReviewInput) (code.Review, error) {
	var out code.Review
	err := transact(ctx, s.pool, s.q, "submitting the review", actor, func(ctx context.Context, q *queries.Queries) error {
		orgID, err := q.CommitOrg(ctx, repoID, sha)
		if errors.Is(err, pgx.ErrNoRows) {
			return repos.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("finding commit %s: %w", sha, err)
		}
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("naming the review: %w", err)
		}
		if _, err := q.InsertReview(ctx, queries.InsertReviewParams{ID: id, OrgID: orgID, RepoID: repoID, Sha: sha, ReviewerID: &actor,
			State: string(in.State), Body: in.Body}); err != nil {
			return fmt.Errorf("recording the review: %w", err)
		}
		row, err := q.ReviewOf(ctx, repoID, id)
		if err != nil {
			return fmt.Errorf("reading review %s back: %w", id, err)
		}
		out = reviewOf(row.Review, row.ReviewerDisplayName, row.ReviewerKind)
		details := map[string]any{"repo": repoID, "sha": sha, "state": in.State}
		if err := orgEvent(ctx, q, &orgID, actor, "review.submitted", id, details); err != nil {
			return err
		}
		return restatusAt(ctx, q, repoID, sha)
	})
	return out, err
}

// reviewsAt lists the reviews on those of the repository's commits that shas name, by commit, each commit's in the
// order they were submitted.
func reviewsAt(ctx context.Context, q *queries.Queries, repoID uuid.UUID, shas []string) (map[string][]code.Review, error) {
	if len(shas) == 0 {
		return nil, nil
	}
	rows, err := q.ReviewsOf(ctx, repoID, shas)
	if err != nil {
		return nil, fmt.Errorf("reading the reviews: %w", err)
	}
	out := map[string][]code.Review{}
	for _, r := range rows {
		out[r.Review.Sha] = append(out[r.Review.Sha], reviewOf(r.Review, r.ReviewerDisplayName, r.ReviewerKind))
	}
	return out, nil
}

// authorsAt lists who is behind those of the repository's commits that shas name, by commit: the principal behind its
// author, when the push worker mapped one, and the pusher who first brought it, while they are there.
func authorsAt(ctx context.Context, q *queries.Queries, repoID uuid.UUID, shas []string) (map[string][]uuid.UUID, error) {
	rows, err := q.HeadAuthors(ctx, repoID, shas)
	if err != nil {
		return nil, fmt.Errorf("reading who is behind the commits: %w", err)
	}
	out := map[string][]uuid.UUID{}
	for _, r := range rows {
		for _, id := range []*uuid.UUID{r.AuthorID, r.PusherID} {
			if id != nil {
				out[r.Sha] = append(out[r.Sha], *id)
			}
		}
	}
	return out, nil
}

// reviewOf is the domain's view of a reviews row, with the display name and kind of its reviewer.
func reviewOf(r queries.Review, displayName, kind *string) code.Review {
	return code.Review{ID: r.ID, SHA: r.Sha, State: code.ReviewState(r.State), Body: r.Body,
		Reviewer: principalOf(r.ReviewerID, displayName), Person: kind != nil && *kind == string(auth.KindUser), SubmittedAt: r.SubmittedAt}
}
