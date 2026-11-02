package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/runner"
)

// followingFile declares a build on every push, a job that follows annotation lint and one that follows approvals.
const followingFile = `jobs:
  ci/build:
    on: {push: }
    script: make
  after-lint:
    on: {annotation: {names: [lint]}}
    script: make lint-report
  after-approval:
    on: {review: {states: [approved]}}
    script: make deploy
`

// queueFixture is a repository whose main, at a, and feature, at b, declare followingFile, with lint In progress on b
// and main's build claimed.
type queueFixture struct {
	*runFixture
	repo uuid.UUID
	run  runner.Run
}

func newQueueFixture(t *testing.T, file string) *queueFixture {
	t.Helper()
	f := &queueFixture{runFixture: newRunFixture(t)}
	ctx := context.Background()
	f.repo = f.website.ID
	f.push(t, pushOf(f.repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}},
		commit(shaA, person("Olivia", "olivia@example.com"))), tip(t, "main", "blob-1", file))
	f.run = f.claim(t)
	feature := pushOf(f.repo, f.olivia, "pack-2", []git.RefUpdate{{Name: "refs/heads/feature", Old: git.ZeroSHA, New: shaB}},
		commit(shaB, person("Olivia", "olivia@example.com"), shaA))
	feature.Facts = []git.Facts{{Branch: "feature", Ahead: 1}}
	f.push(t, feature, tip(t, "feature", "blob-1", file))
	if _, err := f.c.SetAnnotation(ctx, f.olivia, f.repo, shaB, annotation("lint", code.AnnotationInProgress)); err != nil {
		t.Fatal(err)
	}
	return f
}

// TestQueueingLocksTheRunQueueFirst holds a repository's run queue while each transaction that locks a run or may
// queue runs starts, and checks that it waits for the queue before it locks any run, annotation or branch of the
// repository: one that locked them first could wait in a circle with another that holds the queue and waits for them.
// The rows a step would lock first are there before it starts; those it inserts are out of other transactions' sight.
func TestQueueingLocksTheRunQueueFirst(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		step func(context.Context, *queueFixture) error
	}{
		{name: "marking a run running", step: func(ctx context.Context, f *queueFixture) error {
			_, err := f.r.MarkRunning(ctx, f.run.ID)
			return err
		}},
		{name: "ending a run", step: func(ctx context.Context, f *queueFixture) error {
			_, err := f.r.Finish(ctx, f.run.ID, jobs.StateSucceeded, "succeeded")
			return err
		}},
		{name: "putting a run off", step: func(ctx context.Context, f *queueFixture) error {
			return f.r.Defer(ctx, f.run.ID, time.Now().Add(time.Minute), "no room")
		}},
		{name: "cancelling a run", step: func(ctx context.Context, f *queueFixture) error {
			_, err := f.c.CancelRun(ctx, f.olivia, f.repo, f.run.Number)
			return err
		}},
		{name: "setting an annotation a job follows", step: func(ctx context.Context, f *queueFixture) error {
			_, err := f.c.SetAnnotation(ctx, f.olivia, f.repo, shaB, annotation("lint", code.AnnotationSuccessful))
			return err
		}},
		{name: "submitting a review a job follows", step: func(ctx context.Context, f *queueFixture) error {
			_, err := f.c.SubmitReview(ctx, f.olivia, f.repo, shaB, code.ReviewInput{State: code.ReviewApproved})
			return err
		}},
		{name: "processing a push that merges a branch", step: func(ctx context.Context, f *queueFixture) error {
			p := pushOf(f.repo, f.olivia, "", []git.RefUpdate{{Name: "refs/heads/main", Old: shaA, New: shaB}})
			p.Brought = []git.Brought{{Branch: git.DefaultBranch, Heads: []git.Head{{Branch: "feature", SHA: shaB}}}}
			if err := f.g.Record(ctx, p); err != nil {
				return err
			}
			_, err := f.g.Process(ctx, git.RetryAfter)
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newQueueFixture(t, followingFile)
			ctx := context.Background()
			holder := f.holdQueue(t)
			done := make(chan error, 1)
			go func() { done <- tt.step(ctx, f) }()
			f.waitForALock(t)
			f.checkUnlocked(t)
			if err := holder.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Errorf("the step = %v", err)
			}
		})
	}
}

// TestChangesNoJobFollowsSkipTheRunQueue sets an annotation and submits a review that no job follows while the
// repository's run queue is held: neither waits for it.
func TestChangesNoJobFollowsSkipTheRunQueue(t *testing.T) {
	t.Parallel()
	f := newQueueFixture(t, jobsFile)
	holder := f.holdQueue(t)
	defer func() { _ = holder.Rollback(context.Background()) }() // it only held the queue
	// A change that waits for the queue then fails rather than hangs.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := f.c.SetAnnotation(ctx, f.olivia, f.repo, shaB, annotation("lint", code.AnnotationSuccessful)); err != nil {
		t.Errorf("SetAnnotation() = %v", err)
	}
	if _, err := f.c.SubmitReview(ctx, f.olivia, f.repo, shaB, code.ReviewInput{State: code.ReviewApproved}); err != nil {
		t.Errorf("SubmitReview() = %v", err)
	}
}

// holdQueue takes the repository's run queue in a transaction of its own, which the caller ends.
func (f *queueFixture) holdQueue(t *testing.T) interface{ Rollback(context.Context) error } {
	t.Helper()
	ctx := context.Background()
	holder, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Rollback(ctx) }) // ended already, unless the test failed first
	if err := queries.New(holder).LockRunQueue(ctx, f.repo); err != nil {
		t.Fatal(err)
	}
	return holder
}

// waitForALock waits until a session of the test's database waits for a lock.
func (f *queueFixture) waitForALock(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var waiting int
		err := f.pool.QueryRow(context.Background(),
			"SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'").Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			return
		}
	}
	t.Fatal("nothing waited for the run queue")
}

// checkUnlocked checks that no transaction holds a run, an annotation or a branch of the repository.
func (f *queueFixture) checkUnlocked(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // it only locked, to see that it could
	for _, table := range []string{"job_runs", "annotations", "branches"} {
		rows, err := tx.Query(ctx, "SELECT 1 FROM "+table+" WHERE repo_id = $1 FOR UPDATE NOWAIT", f.repo)
		if err == nil {
			for rows.Next() {
			}
			rows.Close()
			err = rows.Err()
		}
		if err != nil {
			t.Fatalf("locking the repository's %s while the run queue is held: %v, want them free", table, err)
		}
	}
}
