package postgres

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/repos"
)

func annotation(name string, status code.AnnotationStatus) code.AnnotationInput {
	return code.AnnotationInput{Name: name, Status: status, Summary: "About " + name, Link: "https://ci.example.com/" + name}
}

func TestCodeStoreSetAnnotation(t *testing.T) {
	f := newCodeFixture(t)
	ctx := context.Background()
	first, err := f.c.SetAnnotation(ctx, f.max, f.website.ID, f.shaD, annotation("ci/build", code.AnnotationInProgress))
	if err != nil {
		t.Fatal(err)
	}
	if first.Principal == nil || first.Principal.ID != f.max || first.Principal.DisplayName == "" || first.Status != code.AnnotationInProgress ||
		first.Summary != "About ci/build" || first.CompletedAt != nil {
		t.Fatalf("SetAnnotation() = %+v", first)
	}
	done, err := f.c.SetAnnotation(ctx, f.max, f.website.ID, f.shaD, annotation("ci/build", code.AnnotationSuccessful))
	if err != nil || done.ID != first.ID || done.Status != code.AnnotationSuccessful || done.CompletedAt == nil || !done.StartedAt.Equal(first.StartedAt) {
		t.Fatalf("SetAnnotation(successful) = %+v, %v; want %s completed, started at %s", done, err, first.ID, first.StartedAt)
	}
	again, err := f.c.SetAnnotation(ctx, f.max, f.website.ID, f.shaD, annotation("ci/build", code.AnnotationSuccessful))
	if err != nil || !again.UpdatedAt.Equal(done.UpdatedAt) || !again.CompletedAt.Equal(*done.CompletedAt) {
		t.Errorf("SetAnnotation(the same) = %+v, %v; want it unchanged, %+v", again, err, done)
	}
	rerun, err := f.c.SetAnnotation(ctx, f.max, f.website.ID, f.shaD, annotation("ci/build", code.AnnotationPending))
	if err != nil || rerun.CompletedAt != nil || !rerun.StartedAt.After(first.StartedAt) {
		t.Errorf("SetAnnotation(pending again) = %+v, %v; want a new run", rerun, err)
	}

	var taken *code.TakenError
	if _, err := f.c.SetAnnotation(ctx, f.nia, f.website.ID, f.shaD, annotation("ci/build", code.AnnotationFailed)); !errors.As(err, &taken) ||
		taken.By == nil || taken.By.ID != f.max {
		t.Errorf("another principal's SetAnnotation() = %v, want it taken by %s", err, f.max)
	}
	if _, err := f.c.SetAnnotation(ctx, f.nia, f.website.ID, shaB, annotation("ci/build", code.AnnotationFailed)); err != nil {
		t.Errorf("SetAnnotation() on another commit = %v", err)
	}
	for name, repo := range map[string]uuid.UUID{"a commit the repository doesn't hold": f.website.ID, "another repository's commit": f.secret.ID} {
		at := f.shaE
		if repo == f.secret.ID {
			at = f.shaD
		}
		if _, err := f.c.SetAnnotation(ctx, f.max, repo, at, annotation("ci/build", code.AnnotationPending)); !errors.Is(err, repos.ErrNotFound) {
			t.Errorf("%s: SetAnnotation() = %v, want %v", name, err, repos.ErrNotFound)
		}
	}

	// Three changes by Max, the unchanged one aside: an insert and two updates.
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'annotations' AND principal_id = $1", f.max); n != 3 {
		t.Errorf("Max's audit rows = %d, want 3", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = 'annotation.set' AND actor_id = $1 AND org_id = $2", f.max, f.acme.ID); n != 3 {
		t.Errorf("Max's annotation.set events = %d, want 3", n)
	}
}

func TestCodeStoreSetAnnotationRace(t *testing.T) {
	f := newCodeFixture(t)
	ctx := context.Background()
	who := []uuid.UUID{f.olivia, f.max, f.nia}
	errs := make([]error, len(who))
	var wg sync.WaitGroup
	for i, p := range who {
		wg.Go(func() {
			_, errs[i] = f.c.SetAnnotation(ctx, p, f.website.ID, f.shaD, annotation("ci/build", code.AnnotationPending))
		})
	}
	wg.Wait()
	var set, taken int
	for _, err := range errs {
		switch {
		case err == nil:
			set++
		case errors.Is(err, code.ErrAnnotationTaken):
			taken++
		default:
			t.Errorf("SetAnnotation() = %v", err)
		}
	}
	if set != 1 || taken != 2 {
		t.Errorf("got %d set and %d taken, want one principal's and the others refused", set, taken)
	}
}

func TestCodeStoreAnnotations(t *testing.T) {
	f := newCodeFixture(t)
	ctx := context.Background()
	for _, in := range []code.AnnotationInput{annotation("ci/test", code.AnnotationFailed), annotation("ci/build", code.AnnotationSuccessful)} {
		if _, err := f.c.SetAnnotation(ctx, f.max, f.website.ID, f.shaD, in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.c.SetAnnotation(ctx, f.max, f.website.ID, shaB, annotation("lint", code.AnnotationPending)); err != nil {
		t.Fatal(err)
	}
	onD, err := f.c.Annotations(ctx, f.website.ID, f.shaD)
	if err != nil || len(onD) != 2 || onD[0].Name != "ci/build" || onD[1].Name != "ci/test" || onD[1].Status != code.AnnotationFailed {
		t.Errorf("Annotations(d) = %+v, %v; want ci/build, then ci/test failed", onD, err)
	}
	if none, err := f.c.Annotations(ctx, f.website.ID, shaA); err != nil || len(none) != 0 {
		t.Errorf("Annotations(a) = %+v, %v; want none", none, err)
	}
	if _, err := f.c.Annotations(ctx, f.website.ID, f.shaE); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Annotations() of no commit = %v, want %v", err, repos.ErrNotFound)
	}

	// Commits come with their annotations, wherever the store reads them.
	main, err := f.c.Branch(ctx, f.website.ID, "main")
	if err != nil || main.Commit == nil || len(main.Commit.Annotations) != 2 {
		t.Errorf("Branch(main) = %+v, %v; want its head's two annotations", main.Commit, err)
	}
	line, err := f.c.FirstParents(ctx, f.website.ID, f.shaD, 10)
	if err != nil || len(line) != 3 {
		t.Fatalf("FirstParents() = %v, %v", names(line), err)
	}
	var counts []int
	for _, c := range line {
		counts = append(counts, len(c.Annotations))
	}
	if !slices.Equal(counts, []int{2, 1, 0}) {
		t.Errorf("FirstParents() annotations = %v, want [2 1 0]", counts)
	}
	c, _, err := f.c.Commit(ctx, f.website.ID, shaB)
	if err != nil || len(c.Annotations) != 1 || c.Annotations[0].Name != "lint" {
		t.Errorf("Commit(b) = %+v, %v; want lint", c.Annotations, err)
	}
}

func TestCodeStoreDeleteAnnotation(t *testing.T) {
	tests := []struct {
		name    string
		actor   func(*codeFixture) uuid.UUID
		anyone  bool
		annName string
		wantErr error
	}{
		{name: "its principal", actor: func(f *codeFixture) uuid.UUID { return f.max }},
		{name: "another principal, holding annotations.delete_any", actor: func(f *codeFixture) uuid.UUID { return f.olivia }, anyone: true},
		{name: "another principal", actor: func(f *codeFixture) uuid.UUID { return f.nia }, wantErr: code.ErrAnnotationTaken},
		{name: "one that isn't there", actor: func(f *codeFixture) uuid.UUID { return f.max }, annName: "ci/test", wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newCodeFixture(t)
			ctx := context.Background()
			if _, err := f.c.SetAnnotation(ctx, f.max, f.website.ID, f.shaD, annotation("ci/build", code.AnnotationFailed)); err != nil {
				t.Fatal(err)
			}
			name := "ci/build"
			if tt.annName != "" {
				name = tt.annName
			}
			actor := tt.actor(f)
			err := f.c.DeleteAnnotation(ctx, actor, f.website.ID, f.shaD, name, tt.anyone)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("DeleteAnnotation() = %v, want %v", err, tt.wantErr)
			}
			f.checkDeleted(t, actor, tt.wantErr == nil)
		})
	}
}

// checkDeleted checks that website's d carries no annotation, deleted by actor and recorded, when deleted is true, and
// that it still carries one otherwise.
func (f *codeFixture) checkDeleted(t *testing.T, actor uuid.UUID, deleted bool) {
	t.Helper()
	left, err := f.c.Annotations(context.Background(), f.website.ID, f.shaD)
	if err != nil || (len(left) == 0) != deleted {
		t.Errorf("Annotations() after = %+v, %v; want it deleted: %t", left, err, deleted)
	}
	want := 0
	if deleted {
		want = 1
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = 'annotation.deleted' AND actor_id = $1", actor); n != want {
		t.Errorf("annotation.deleted events = %d, want %d", n, want)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'annotations' AND action = 'delete' AND principal_id = $1", actor); n != want {
		t.Errorf("audited deletions = %d, want %d", n, want)
	}
}
