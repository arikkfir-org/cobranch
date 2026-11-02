package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

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
