package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/repos"
)

// Reviews lists at most limit of the reviews on one of the repository's commits in the order they were submitted, after
// the one named after unless it is uuid.Nil, or repos.ErrNotFound when it holds no such commit (code.Store).
func (s *CodeStore) Reviews(ctx context.Context, repoID uuid.UUID, sha string, after uuid.UUID, limit int) ([]code.Review, error) {
	if _, err := s.q.CommitOrg(ctx, repoID, sha); errors.Is(err, pgx.ErrNoRows) {
		return nil, repos.ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("finding commit %s: %w", sha, err)
	}
	params := queries.ReviewsPageParams{RepoID: repoID, Sha: sha, Lim: int32(min(limit, code.MaxPageSize+1))}
	if after != uuid.Nil {
		params.After = &after
	}
	rows, err := s.q.ReviewsPage(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("reading the reviews of commit %s: %w", sha, err)
	}
	out := make([]code.Review, 0, len(rows))
	for _, r := range rows {
		out = append(out, reviewOf(r.Review, r.ReviewerDisplayName, r.ReviewerKind))
	}
	return out, nil
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

// SubmitReview records actor's review of the repository's commit in one transaction that acts for actor: it opens the
// review's comments as threads of the chat of the branch it names, appends event review.submitted and works out again
// the status of the branches at the commit (code.Store). A branch deleted since the service found it is
// chats.ErrBranchDeleted.
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
		follows, err := lockForTriggers(ctx, q, repoID, sha, jobs.EventReview)
		if err != nil {
			return err
		}
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("naming the review: %w", err)
		}
		if _, err := q.InsertReview(ctx, queries.InsertReviewParams{ID: id, OrgID: orgID, RepoID: repoID, Sha: sha, ReviewerID: &actor,
			State: string(in.State), Body: in.Body}); err != nil {
			return fmt.Errorf("recording the review: %w", err)
		}
		if err := openComments(ctx, q, actor, repoID, id, in); err != nil {
			return err
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
		if err := restatusAt(ctx, q, repoID, sha); err != nil || !follows {
			return err
		}
		return queueBranchRuns(ctx, q, jobs.Event{Kind: jobs.EventReview, RepoID: repoID, SHA: sha, State: string(in.State), ActorID: actor})
	})
	return out, err
}

// openComments opens the comments of the review reviewID names as threads of the chat of the repository's branch that
// in names, which it holds for the rest of the transaction, each anchored with its first message, and appends their
// events. A branch that is gone is chats.ErrBranchDeleted.
func openComments(ctx context.Context, q *queries.Queries, actor, repoID, reviewID uuid.UUID, in code.ReviewInput) error {
	if len(in.Comments) == 0 {
		return nil
	}
	row, err := q.ChatSubject(ctx, repoID, in.Branch)
	if errors.Is(err, pgx.ErrNoRows) {
		return chats.ErrBranchDeleted
	}
	if err != nil {
		return fmt.Errorf("finding branch %s: %w", in.Branch, err)
	}
	subj := chatSubjectOf(row)
	if err := holdChat(ctx, q, subj); err != nil {
		return err
	}
	for _, c := range in.Comments {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("naming the comment's thread: %w", err)
		}
		messageID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("naming the comment's message: %w", err)
		}
		if _, err := insertThread(ctx, q, chats.Poster{ID: actor}, subj, id, messageID, &c.Anchor, &reviewID, chats.Said{Body: c.Body}); err != nil {
			return err
		}
		if err := chatChanged(ctx, q, actor, subj, chats.ThreadStarted, chatEvent{Thread: id, Message: &messageID}); err != nil {
			return err
		}
	}
	return nil
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
