package postgres

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/repos"
)

func annotation(name string, status code.AnnotationStatus) code.AnnotationInput {
	return code.AnnotationInput{Name: name, Status: status, Summary: "About " + name, Link: "https://ci.example.com/" + name}
}

// from is in, made from version of the annotation.
func from(version int64, in code.AnnotationInput) code.AnnotationInput {
	in.Version = version
	return in
}

func TestCodeStoreSetAnnotation(t *testing.T) {
	t.Parallel()
	f := newCodeFixture(t)
	ctx := context.Background()
	first, err := f.c.SetAnnotation(ctx, f.max, f.website.ID, f.shaD, annotation("ci/build", code.AnnotationInProgress))
	if err != nil {
		t.Fatal(err)
	}
	if first.Principal == nil || first.Principal.ID != f.max || first.Principal.DisplayName == "" || first.Status != code.AnnotationInProgress ||
		first.Summary != "About ci/build" || first.CompletedAt != nil || first.Version != 1 {
		t.Fatalf("SetAnnotation() = %+v", first)
	}
	done, err := f.c.SetAnnotation(ctx, f.max, f.website.ID, f.shaD, annotation("ci/build", code.AnnotationSuccessful))
	if err != nil || done.ID != first.ID || done.Status != code.AnnotationSuccessful || done.CompletedAt == nil || !done.StartedAt.Equal(first.StartedAt) ||
		done.Version != 2 {
		t.Fatalf("SetAnnotation(successful) = %+v, %v; want %s completed at version 2, started at %s", done, err, first.ID, first.StartedAt)
	}
	again, err := f.c.SetAnnotation(ctx, f.max, f.website.ID, f.shaD, annotation("ci/build", code.AnnotationSuccessful))
	if err != nil || !again.UpdatedAt.Equal(done.UpdatedAt) || !again.CompletedAt.Equal(*done.CompletedAt) || again.Version != 2 {
		t.Errorf("SetAnnotation(the same) = %+v, %v; want it unchanged, %+v", again, err, done)
	}
	rerun, err := f.c.SetAnnotation(ctx, f.max, f.website.ID, f.shaD, from(2, annotation("ci/build", code.AnnotationPending)))
	if err != nil || rerun.CompletedAt != nil || !rerun.StartedAt.After(first.StartedAt) || rerun.Version != 3 {
		t.Errorf("SetAnnotation(pending again, from version 2) = %+v, %v; want a new run at version 3", rerun, err)
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
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = 'annotation.changed' AND details->>'action' = 'set' AND actor_id = $1 AND org_id = $2", f.max, f.acme.ID); n != 3 {
		t.Errorf("Max's annotation.changed events setting it = %d, want 3", n)
	}
}

func TestCodeStoreSetAnnotationVersions(t *testing.T) {
	t.Parallel()
	f := newCodeFixture(t)
	ctx := context.Background()
	if _, err := f.c.SetAnnotation(ctx, f.max, f.website.ID, f.shaD, annotation("ci/build", code.AnnotationPending)); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		in      code.AnnotationInput
		wantErr error
	}{
		{name: "from the version it is", in: from(1, annotation("ci/build", code.AnnotationFailed))},
		{name: "from a version since changed", in: from(1, annotation("ci/build", code.AnnotationSuccessful)), wantErr: code.ErrAnnotationChanged},
		{name: "from any version", in: from(code.AnyVersion, annotation("ci/build", code.AnnotationSuccessful))},
		{name: "from a version of none there", in: from(1, annotation("ci/test", code.AnnotationFailed)), wantErr: code.ErrAnnotationChanged},
		{name: "from any version of none there", in: from(code.AnyVersion, annotation("ci/test", code.AnnotationFailed)), wantErr: code.ErrAnnotationChanged},
		{name: "a new one where it is", in: from(code.NoAnnotation, annotation("ci/build", code.AnnotationFailed)), wantErr: code.ErrAnnotationChanged},
		{name: "a new one where none is", in: from(code.NoAnnotation, annotation("ci/test", code.AnnotationFailed))},
	}
	for _, tt := range tests { // in order: each changes what the next meets
		if _, err := f.c.SetAnnotation(ctx, f.max, f.website.ID, f.shaD, tt.in); !errors.Is(err, tt.wantErr) {
			t.Errorf("SetAnnotation() %s = %v, want %v", tt.name, err, tt.wantErr)
		}
	}
	if got, err := f.c.Annotations(ctx, f.website.ID, f.shaD); err != nil || len(got) != 2 || got[0].Version != 3 || got[0].Status != code.AnnotationSuccessful ||
		got[1].Version != 1 {
		t.Errorf("Annotations() = %+v, %v; want ci/build successful at version 3, and ci/test new", got, err)
	}
}

func TestCodeStoreSetAnnotationRace(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	tests := []struct {
		name    string
		actor   func(*codeFixture) uuid.UUID
		anyone  bool
		annName string
		version int64
		at      func(*codeFixture) string
		wantErr error
	}{
		{name: "its principal", actor: func(f *codeFixture) uuid.UUID { return f.max }},
		{name: "another principal, holding annotations.delete_any", actor: func(f *codeFixture) uuid.UUID { return f.olivia }, anyone: true},
		{name: "another principal", actor: func(f *codeFixture) uuid.UUID { return f.nia }, wantErr: code.ErrAnnotationTaken},
		{name: "at the version it is", actor: func(f *codeFixture) uuid.UUID { return f.max }, version: 1},
		{name: "at another version", actor: func(f *codeFixture) uuid.UUID { return f.max }, version: 2, wantErr: code.ErrAnnotationChanged},
		{name: "a version of one that isn't there", actor: func(f *codeFixture) uuid.UUID { return f.max }, annName: "ci/test", version: 1, wantErr: code.ErrAnnotationChanged},
		{name: "a version on a commit the repository doesn't hold", actor: func(f *codeFixture) uuid.UUID { return f.max }, version: 1,
			at: func(f *codeFixture) string { return f.shaE }, wantErr: repos.ErrNotFound},
		{name: "one that isn't there", actor: func(f *codeFixture) uuid.UUID { return f.max }, annName: "ci/test", wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newCodeFixture(t)
			ctx := context.Background()
			if _, err := f.c.SetAnnotation(ctx, f.max, f.website.ID, f.shaD, annotation("ci/build", code.AnnotationFailed)); err != nil {
				t.Fatal(err)
			}
			name := "ci/build"
			if tt.annName != "" {
				name = tt.annName
			}
			actor, at := tt.actor(f), f.shaD
			if tt.at != nil {
				at = tt.at(f)
			}
			err := f.c.DeleteAnnotation(ctx, actor, f.website.ID, at, name, tt.version, tt.anyone)
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
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = 'annotation.changed' AND details->>'action' = 'deleted' AND actor_id = $1", actor); n != want {
		t.Errorf("annotation.changed events deleting it = %d, want %d", n, want)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'annotations' AND action = 'delete' AND principal_id = $1", actor); n != want {
		t.Errorf("audited deletions = %d, want %d", n, want)
	}
}

// TestCodeStoreSetAnnotationRacesDelete sets an annotation the moment another principal's of its name is there and
// being deleted: the set waits for the deletion, then sets its own.
func TestCodeStoreSetAnnotationRacesDelete(t *testing.T) {
	t.Parallel()
	f := newCodeFixture(t)
	ctx := context.Background()
	// Nia's insert waits, before it meets the annotation there, for the advisory lock that gate holds.
	execAll(t, f.pool, `CREATE FUNCTION hold_nia() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.created_by = '`+f.nia.String()+`' THEN PERFORM pg_advisory_xact_lock_shared(1); END IF;
    RETURN NEW;
END $$`, "CREATE TRIGGER hold_nia BEFORE INSERT ON annotations FOR EACH ROW EXECUTE FUNCTION hold_nia()")
	gate := acquire(t, f.pool)
	execAll(t, gate, "SELECT pg_advisory_lock(1)")

	set := make(chan error, 1)
	go func() {
		_, err := f.c.SetAnnotation(ctx, f.nia, f.website.ID, f.shaD, annotation("ci/build", code.AnnotationPending))
		set <- err
	}()
	waitForLock(t, f.pool, "advisory")
	if _, err := f.c.SetAnnotation(ctx, f.max, f.website.ID, f.shaD, annotation("ci/build", code.AnnotationFailed)); err != nil {
		t.Fatal(err)
	}
	deleter, err := acquire(t, f.pool).Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deleter.Exec(ctx, "SELECT id FROM annotations WHERE name = 'ci/build' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	execAll(t, gate, "SELECT pg_advisory_unlock(1)")
	waitForLock(t, f.pool, "transactionid", "tuple") // Nia's set waits for the deletion
	if _, err := deleter.Exec(ctx, "DELETE FROM annotations WHERE name = 'ci/build'"); err != nil {
		t.Fatal(err)
	}
	if err := deleter.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if err := <-set; err != nil {
		t.Fatalf("SetAnnotation() racing a delete = %v, want Nia's set", err)
	}
	if got, err := f.c.Annotations(ctx, f.website.ID, f.shaD); err != nil || len(got) != 1 || got[0].Principal == nil || got[0].Principal.ID != f.nia {
		t.Errorf("Annotations() = %+v, %v; want Nia's", got, err)
	}
}

// acquire is a connection of pool's of the test's own, released when it ends.
func acquire(t *testing.T, pool *pgxpool.Pool) *pgxpool.Conn {
	t.Helper()
	c, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Release)
	return c
}

// execAll runs each statement on db.
func execAll(t *testing.T, db interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, statements ...string) {
	t.Helper()
	for _, s := range statements {
		if _, err := db.Exec(context.Background(), s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// waitForLock waits until a session of pool's database waits for a lock of one of the kinds that events name.
func waitForLock(t *testing.T, pool *pgxpool.Pool, events ...string) {
	t.Helper()
	for range 500 {
		if count(t, pool, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND wait_event = ANY ($1)`, events) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no session waits for a lock of kind %v", events)
}
