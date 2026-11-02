package postgres

import (
	"cmp"
	"context"
	"slices"
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

// queueingSteps are the transactions that lock a run or an annotation, or may queue runs, each in a repository whose
// branches declare file.
var queueingSteps = []struct {
	name string
	file string // followingFile when empty
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
	{name: "setting a job's annotation where no job follows annotations", file: jobsFile, step: func(ctx context.Context, f *queueFixture) error {
		_, err := f.c.SetAnnotation(ctx, f.olivia, f.repo, shaB, annotation("release", code.AnnotationSuccessful))
		return err
	}},
	{name: "deleting an annotation", step: func(ctx context.Context, f *queueFixture) error {
		return f.c.DeleteAnnotation(ctx, f.olivia, f.repo, shaB, "lint", 0, false)
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

// TestQueueingLocksTheRunQueueFirst holds a repository's run queue while each transaction that locks a run or an
// annotation, or may queue runs, starts, and checks that it waits for the queue before it locks any run, annotation or
// branch of the repository: one that locked them first could wait in a circle with another that holds the queue and
// waits for them, as a change of the annotation of a job's name that no job follows could with a run that ends and
// queues that job. The rows a step would lock first are there before it starts; those it inserts are out of other
// transactions' sight.
func TestQueueingLocksTheRunQueueFirst(t *testing.T) {
	t.Parallel()
	for _, tt := range queueingSteps {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			newQueueFixture(t, cmp.Or(tt.file, followingFile)).checkQueuesFirst(t, tt.step)
		})
	}
}

// TestReportingAWebhookRunLocksTheRunQueueFirst has a webhook's receiver report Successful while the repository's run
// queue is held: the report, which ends the run, waits for the queue before it locks the run or its annotation.
func TestReportingAWebhookRunLocksTheRunQueueFirst(t *testing.T) {
	t.Parallel()
	f := &queueFixture{runFixture: newRunFixture(t)}
	ctx := context.Background()
	f.repo = f.website.ID
	f.push(t, pushOf(f.repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}},
		commit(shaA, person("Olivia", "olivia@example.com"))), tip(t, "main", "blob-1", hookFile))
	f.run = f.claim(t)
	if marked, err := f.r.MarkRunning(ctx, f.run.ID); !marked || err != nil {
		t.Fatalf("MarkRunning() = %t, %v", marked, err)
	}
	f.checkQueuesFirst(t, func(ctx context.Context, f *queueFixture) error {
		in := code.AnnotationInput{Name: "deploy", Status: code.AnnotationSuccessful, Version: code.AnyVersion}
		_, err := f.c.SetAnnotation(ctx, f.run.JobID, f.repo, shaA, in)
		return err
	})
	if got, want := f.runs(t, f.repo), []string{"#1 deploy main@a succeeded: its webhook's receiver reported successful"}; !slices.Equal(got, want) {
		t.Errorf("runs = %q, want %q", got, want)
	}
}

// TestReviewsNoJobFollowsSkipTheRunQueue submits a review that no job follows while the repository's run queue is
// held: it doesn't wait for it, since it locks no run or annotation.
func TestReviewsNoJobFollowsSkipTheRunQueue(t *testing.T) {
	t.Parallel()
	f := newQueueFixture(t, jobsFile)
	holder := f.holdQueue(t)
	defer func() { _ = holder.Rollback(context.Background()) }() // it only held the queue
	// A change that waits for the queue then fails rather than hangs.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := f.c.SubmitReview(ctx, f.olivia, f.repo, shaB, code.ReviewInput{State: code.ReviewApproved}); err != nil {
		t.Errorf("SubmitReview() = %v", err)
	}
}

// checkQueuesFirst holds the repository's run queue while step starts, checks that step waits for it before it locks
// any run, annotation or branch of the repository, then that step succeeds once the queue is free.
func (f *queueFixture) checkQueuesFirst(t *testing.T, step func(context.Context, *queueFixture) error) {
	t.Helper()
	ctx := context.Background()
	holder := f.holdQueue(t)
	done := make(chan error, 1)
	go func() { done <- step(ctx, f) }()
	f.waitForALock(t)
	f.checkUnlocked(t)
	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Errorf("the step = %v", err)
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
