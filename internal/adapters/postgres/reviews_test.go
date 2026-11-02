package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/repos"
)

func TestCodeStoreSubmitReview(t *testing.T) {
	t.Parallel()
	f := newCodeFixture(t)
	ctx := context.Background()
	changes, err := f.c.SubmitReview(ctx, f.max, f.website.ID, f.shaD, code.ReviewInput{State: code.ReviewChangesRequested, Body: "Rename the handler"})
	if err != nil {
		t.Fatal(err)
	}
	if changes.Reviewer == nil || changes.Reviewer.ID != f.max || changes.Reviewer.DisplayName == "" || !changes.Person || changes.SHA != f.shaD ||
		changes.State != code.ReviewChangesRequested || changes.Body != "Rename the handler" || changes.SubmittedAt.IsZero() {
		t.Fatalf("SubmitReview() = %+v", changes)
	}
	approved, err := f.c.SubmitReview(ctx, f.max, f.website.ID, f.shaD, code.ReviewInput{State: code.ReviewApproved})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.c.SubmitReview(ctx, f.nia, f.website.ID, shaB, code.ReviewInput{State: code.ReviewNeutral}); err != nil {
		t.Errorf("SubmitReview() on another commit = %v", err)
	}

	list, err := f.c.Reviews(ctx, f.website.ID, f.shaD, uuid.Nil, 10)
	if err != nil || len(list) != 2 || list[0].ID != changes.ID || list[1].ID != approved.ID {
		t.Errorf("Reviews(d) = %+v, %v; want the two of Max's, in order", list, err)
	}
	c, _, err := f.c.Commit(ctx, f.website.ID, f.shaD)
	if err != nil || len(c.Reviews) != 2 || c.Reviews[1].ID != approved.ID {
		t.Errorf("Commit(d).Reviews = %+v, %v; want the two of Max's", c.Reviews, err)
	}
	if got, err := f.c.Review(ctx, f.website.ID, approved.ID); err != nil || got.ID != approved.ID || got.State != code.ReviewApproved {
		t.Errorf("Review() = %+v, %v; want %+v", got, err, approved)
	}

	for name, call := range map[string]func() error{
		"a review of a commit the repository doesn't hold": func() error {
			_, err := f.c.SubmitReview(ctx, f.max, f.website.ID, f.shaE, code.ReviewInput{State: code.ReviewApproved})
			return err
		},
		"another repository's commit": func() error {
			_, err := f.c.SubmitReview(ctx, f.max, f.secret.ID, f.shaD, code.ReviewInput{State: code.ReviewApproved})
			return err
		},
		"the reviews of a commit the repository doesn't hold": func() error { _, err := f.c.Reviews(ctx, f.website.ID, f.shaE, uuid.Nil, 10); return err },
		"another repository's review":                         func() error { _, err := f.c.Review(ctx, f.secret.ID, approved.ID); return err },
		"a review that isn't there":                           func() error { _, err := f.c.Review(ctx, f.website.ID, uuid.New()); return err },
	} {
		if err := call(); !errors.Is(err, repos.ErrNotFound) {
			t.Errorf("%s: %v, want %v", name, err, repos.ErrNotFound)
		}
	}

	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'reviews' AND principal_id = $1", f.max); n != 2 {
		t.Errorf("Max's audit rows = %d, want 2", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = 'review.submitted' AND actor_id = $1 AND org_id = $2", f.max, f.acme.ID); n != 2 {
		t.Errorf("Max's review.submitted events = %d, want 2", n)
	}
}

func TestCodeStoreReviewsPages(t *testing.T) {
	t.Parallel()
	f := newCodeFixture(t)
	ctx := context.Background()
	var ids []uuid.UUID
	for _, state := range []code.ReviewState{code.ReviewNeutral, code.ReviewApproved} {
		r, err := f.c.SubmitReview(ctx, f.max, f.website.ID, f.shaD, code.ReviewInput{State: state, Body: "Reviewed"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	other, err := f.c.SubmitReview(ctx, f.nia, f.website.ID, shaB, code.ReviewInput{State: code.ReviewApproved})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		after uuid.UUID
		limit int
		want  []uuid.UUID
	}{
		{name: "every one", limit: 10, want: ids},
		{name: "the first page", limit: 1, want: ids[:1]},
		{name: "after the first", after: ids[0], limit: 1, want: ids[1:]},
		{name: "after the last", after: ids[1], limit: 10},
		{name: "after another commit's", after: other.ID, limit: 10},
		{name: "after one that isn't there", after: uuid.New(), limit: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.c.Reviews(ctx, f.website.ID, f.shaD, tt.after, tt.limit)
			found := make([]uuid.UUID, 0, len(got))
			for _, r := range got {
				found = append(found, r.ID)
			}
			if err != nil || !slices.Equal(found, tt.want) {
				t.Errorf("Reviews(d) = %v, %v; want %v", found, err, tt.want)
			}
		})
	}
}

func TestCodeStoreReviewComments(t *testing.T) {
	t.Parallel()
	f := newChatFixture(t)
	ctx := context.Background()
	in := code.ReviewInput{State: code.ReviewChangesRequested, Branch: "feature", Comments: []code.ReviewComment{
		{Anchor: code.Anchor{SHA: shaC, Path: "src/main.go", StartLine: 3, EndLine: 5}, Body: "Rename *this*"},
		{Anchor: code.Anchor{SHA: shaA, Path: "README.md", StartLine: 1, EndLine: 1}, Body: "Typo"},
	}}
	review, err := f.c.SubmitReview(ctx, f.max, f.website.ID, shaC, in)
	if err != nil {
		t.Fatal(err)
	}
	threads, _, err := f.s.Threads(ctx, f.feature, uuid.Nil, 10)
	if err != nil || len(threads) != 2 {
		t.Fatalf("Threads() = %+v, %v; want the review's two comments", threads, err)
	}
	for i, th := range threads { // newest first
		c := in.Comments[len(in.Comments)-1-i]
		if th.ReviewID == nil || *th.ReviewID != review.ID || th.ReviewedSHA != shaC || th.Anchor == nil || *th.Anchor != c.Anchor || th.StartedBy == nil || th.StartedBy.ID != f.max ||
			len(th.Messages) != 1 || th.Messages[0].Body != c.Body || th.Messages[0].Author == nil || th.Messages[0].Author.ID != f.max {
			t.Errorf("thread %d = %+v, want comment %+v of review %s", i, th, c, review.ID)
		}
	}
	if updates, err := f.s.Updates(ctx, f.feature, 0, 10); err != nil || len(updates) != 2 || updates[0].Kind != chats.ThreadStarted ||
		updates[0].Thread.ID != threads[1].ID || updates[0].Message == nil {
		t.Errorf("Updates() = %+v, %v; want the two threads started, in order", updates, err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'chat_threads' AND principal_id = $1", f.max); n != 2 {
		t.Errorf("Max's chat_threads audit rows = %d, want 2", n)
	}

	// A review that is gone leaves its comments in the chat, with their answers.
	if _, err := f.pool.Exec(ctx, "DELETE FROM reviews WHERE id = $1", review.ID); err != nil {
		t.Fatal(err)
	}
	if kept, err := f.s.Thread(ctx, f.feature, threads[0].ID); err != nil || kept.ReviewID != nil || kept.ReviewedSHA != "" || len(kept.Messages) != 1 {
		t.Errorf("Thread() of a review that is gone = %+v, %v; want it kept, of no review", kept, err)
	}

	// A branch deleted since the service found it refuses the comments, and the review with them.
	if _, err := f.pool.Exec(ctx, "UPDATE branches SET deleted_at = now() WHERE id = $1", f.feature.BranchID); err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"feature", "nowhere"} {
		in.Branch = branch
		if _, err := f.c.SubmitReview(ctx, f.max, f.website.ID, shaC, in); !errors.Is(err, chats.ErrBranchDeleted) {
			t.Errorf("SubmitReview() with comments on %s = %v, want %v", branch, err, chats.ErrBranchDeleted)
		}
	}
	if n := count(t, f.pool, "SELECT count(*) FROM reviews WHERE repo_id = $1", f.website.ID); n != 0 {
		t.Errorf("reviews = %d, want none of the refused", n)
	}
}
