package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/repos"
)

func TestCodeStoreSubmitReview(t *testing.T) {
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

	list, err := f.c.Reviews(ctx, f.website.ID, f.shaD)
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
		"the reviews of a commit the repository doesn't hold": func() error { _, err := f.c.Reviews(ctx, f.website.ID, f.shaE); return err },
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
