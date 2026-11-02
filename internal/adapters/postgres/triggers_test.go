package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// manualFile declares a job members start by hand or by an event, and one on every push.
const manualFile = `jobs:
  deploy:
    on: {manual: {}, event: {names: [deploy/requested]}}
    script: make deploy
  build:
    on: {push: }
    script: make
`

// mainAt has Olivia create main at shaA with content as its /cobranch.yaml.
func (f *jobsFixture) mainAt(t *testing.T, content string) {
	t.Helper()
	f.push(t, pushOf(f.website.ID, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}},
		commit(shaA, person("Olivia", "olivia@example.com"))), tip(t, "main", "blob-1", content))
}

// runOf finds one of the website's runs by number, which must be there.
func (f *jobsFixture) runOf(ctx context.Context, t *testing.T, number int64) code.Run {
	t.Helper()
	r, err := f.c.Run(ctx, f.website.ID, number)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCodeStoreRunsManualJobs(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	ctx, repo := context.Background(), f.website.ID
	f.mainAt(t, manualFile)
	if b, err := f.c.Branch(ctx, repo, "main"); err != nil || !slices.Equal(b.ManualJobs, []string{"deploy"}) {
		t.Fatalf("main's manual jobs = %q, %v, want deploy", b.ManualJobs, err)
	}
	r, err := f.c.RunJob(ctx, f.olivia, repo, "main", "deploy")
	if err != nil || r.Number != 2 || r.Trigger.Kind != jobs.EventManual || r.Trigger.ActorID != f.olivia || r.Attempt != 1 {
		t.Fatalf("RunJob() = %+v, %v, want run #2, manual, by Olivia", r, err)
	}
	for _, tt := range []struct {
		name, branch, job string
		want              error
	}{
		{"a run that hasn't ended", "main", "deploy", code.ErrRunActive},
		{"a job without a manual trigger", "main", "build", code.ErrNotManual},
		{"a job nobody declares", "main", "nope", repos.ErrNotFound},
		{"a branch that isn't there", "gone", "deploy", repos.ErrNotFound},
	} {
		if _, err := f.c.RunJob(ctx, f.olivia, repo, tt.branch, tt.job); !errors.Is(err, tt.want) {
			t.Errorf("%s: RunJob() = %v, want %v", tt.name, err, tt.want)
		}
	}
	want := []string{"#1 build main@a queued", "#2 deploy main@a queued"}
	if got := f.runs(t, repo); !slices.Equal(got, want) {
		t.Errorf("runs = %q, want %q", got, want)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = 'job.run' AND actor_id = $1", f.olivia); n != 1 {
		t.Errorf("%d job.run events by Olivia, want 1", n)
	}
}

func TestCodeStorePostsEvents(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	ctx, repo := context.Background(), f.website.ID
	f.mainAt(t, manualFile)
	f.push(t, pushOf(repo, f.olivia, "pack-2", []git.RefUpdate{{Name: "refs/heads/feature", Old: git.ZeroSHA, New: shaB}},
		commit(shaB, person("Olivia", "olivia@example.com"), shaA)), tip(t, "feature", "blob-2", "jobs: {}\n"))
	data := json.RawMessage(`{"env":"production"}`)
	runs, err := f.c.PostEvent(ctx, f.max, repo, code.EventInput{Name: "deploy/requested", Branch: "feature", Data: data})
	var got map[string]string
	if err != nil || len(runs) != 1 || runs[0].Ref != "refs/heads/feature" || runs[0].SHA != shaB ||
		json.Unmarshal(runs[0].Trigger.Data, &got) != nil || got["env"] != "production" {
		t.Fatalf("PostEvent() = %+v, %v, want main's deploy at feature's head with the data", runs, err)
	}
	if again, err := f.c.PostEvent(ctx, f.max, repo, code.EventInput{Name: "deploy/requested", Branch: "feature"}); err != nil || len(again) != 0 {
		t.Errorf("PostEvent() again = %+v, %v, want nothing while the first runs", again, err)
	}
	if other, err := f.c.PostEvent(ctx, f.max, repo, code.EventInput{Name: "other", Branch: "main"}); err != nil || len(other) != 0 {
		t.Errorf("PostEvent(other) = %+v, %v, want nothing", other, err)
	}
	var verr *orgs.ValidationError
	if _, err := f.c.PostEvent(ctx, f.max, repo, code.EventInput{Name: "deploy/requested", Branch: "gone"}); !errors.As(err, &verr) || verr.Field != "branch" {
		t.Errorf("PostEvent(gone) = %v, want the branch refused", err)
	}
	want := []string{"#1 build main@a queued", "#2 deploy feature@b queued"}
	if got := f.runs(t, repo); !slices.Equal(got, want) {
		t.Errorf("runs = %q, want %q", got, want)
	}
}

func TestCodeStoreRerunsRuns(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	ctx, repo := context.Background(), f.website.ID
	f.mainAt(t, manualFile)
	if _, err := f.c.RerunRun(ctx, f.olivia, repo, 1); !errors.Is(err, code.ErrRunActive) {
		t.Errorf("RerunRun(queued) = %v, want ErrRunActive", err)
	}
	cancel := func(n int64) {
		if _, err := f.c.CancelRun(ctx, f.olivia, repo, n); err != nil {
			t.Fatal(err)
		}
	}
	cancel(1)
	second, err := f.c.RerunRun(ctx, f.max, repo, 1)
	if err != nil || second.Number != 2 || second.Attempt != 2 || second.RerunOf != 1 || second.StartedBy == nil || second.StartedBy.ID != f.max ||
		second.State != jobs.StateQueued || second.Trigger.Kind != jobs.EventPush {
		t.Fatalf("RerunRun(#1) = %+v, %v, want #2, attempt 2 of #1, by Max, queued with #1's trigger", second, err)
	}
	if _, err := f.c.RerunRun(ctx, f.max, repo, 1); !errors.Is(err, code.ErrRunActive) {
		t.Errorf("RerunRun(#1) while #2 runs = %v, want ErrRunActive", err)
	}
	cancel(2)
	if third, err := f.c.RerunRun(ctx, f.max, repo, 2); err != nil || third.Attempt != 3 || third.RerunOf != 1 {
		t.Errorf("RerunRun(#2) = %+v, %v, want attempt 3 of #1", third, err)
	}
	if got := f.note(t, repo, shaA, "build"); got != "pending: Queued by build" {
		t.Errorf("build's annotation = %q, want Pending again", got)
	}
	if _, err := f.c.RerunRun(ctx, f.max, repo, 9); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("RerunRun(#9) = %v, want ErrNotFound", err)
	}
}

// chainFile declares a build on every push, a deploy that follows the build's annotation, QA's or a person's check,
// and QA that follows the deploy's: deploy and QA would trigger each other for ever.
const chainFile = `jobs:
  ci/build:
    on: {push: }
    script: make
  deploy:
    on: {annotation: {names: [ci/build, qa, check]}}
    script: make deploy
  qa:
    on: {annotation: {names: [deploy]}}
    script: make qa
`

func TestAnnotationsTriggerRunsInChains(t *testing.T) {
	t.Parallel()
	f := newRunFixture(t)
	ctx, repo := context.Background(), f.website.ID
	f.mainAt(t, chainFile)
	finish := func(n int64) {
		if _, err := f.r.Finish(ctx, f.runOf(ctx, t, n).ID, jobs.StateSucceeded, "succeeded"); err != nil {
			t.Fatal(err)
		}
	}
	finish(1)
	finish(2)
	finish(3)
	want := []string{"#1 ci/build main@a succeeded: succeeded", "#2 deploy main@a succeeded: succeeded", "#3 qa main@a succeeded: succeeded"}
	if got := f.runs(t, repo); !slices.Equal(got, want) {
		t.Fatalf("runs = %q, want deploy after the build, QA after deploy, and no deploy after QA", got)
	}
	if deploy, qa := f.runOf(ctx, t, 2), f.runOf(ctx, t, 3); deploy.CauseRun != 1 || qa.CauseRun != 2 || deploy.Trigger.Kind != jobs.EventAnnotation {
		t.Errorf("causes = #%d, #%d, want #1 and #2", deploy.CauseRun, qa.CauseRun)
	}
	f.push(t, pushOf(repo, f.nia, "pack-2", []git.RefUpdate{{Name: "refs/heads/outside", Old: git.ZeroSHA, New: shaB}},
		commit(shaB, person("Nia", "nia@example.com"), shaA)), tip(t, "outside", "blob-1", chainFile))
	if _, err := f.c.SetAnnotation(ctx, f.max, repo, shaB, code.AnnotationInput{Name: "check", Status: code.AnnotationPending}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.c.SetAnnotation(ctx, f.max, repo, shaB, code.AnnotationInput{Name: "check", Status: code.AnnotationSuccessful, Version: 1}); err != nil {
		t.Fatal(err)
	}
	want = append(want, "#4 ci/build outside@b waiting: waits for a member to start it", "#5 deploy outside@b waiting: waits for a member to start it")
	if got := f.runs(t, repo); !slices.Equal(got, want) {
		t.Errorf("runs = %q, want %q: a person's check triggers deploy, which waits on an outsider's tip", got, want)
	}
}

func TestReviewsTriggerRuns(t *testing.T) {
	t.Parallel()
	f := newJobsFixture(t)
	ctx, repo := context.Background(), f.website.ID
	f.mainAt(t, "jobs:\n  merge:\n    on: {review: {}}\n    script: make merge\n")
	f.push(t, pushOf(repo, f.olivia, "pack-2", []git.RefUpdate{{Name: "refs/heads/feature", Old: git.ZeroSHA, New: shaB}},
		commit(shaB, person("Olivia", "olivia@example.com"), shaA)), tip(t, "feature", "blob-1", "jobs:\n  merge:\n    on: {review: {}}\n    script: make merge\n"))
	for _, state := range []code.ReviewState{code.ReviewChangesRequested, code.ReviewApproved} {
		if _, err := f.c.SubmitReview(ctx, f.max, repo, shaB, code.ReviewInput{State: state}); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"#1 merge feature@b queued"}
	if got := f.runs(t, repo); !slices.Equal(got, want) {
		t.Errorf("runs = %q, want %q: an approval's run alone", got, want)
	}
	if r := f.runOf(ctx, t, 1); r.Trigger.Kind != jobs.EventReview || r.Trigger.State != "approved" || r.Trigger.ActorID != f.max {
		t.Errorf("trigger = %+v, want Max's approval", r.Trigger)
	}
}

func TestRunStoreQueuesScheduleSlots(t *testing.T) {
	t.Parallel()
	f := newRunFixture(t)
	ctx, repo := context.Background(), f.website.ID
	f.mainAt(t, "jobs:\n  nightly:\n    on: {schedule: [{cron: \"0 3 * * *\"}]}\n    script: make nightly\n  build:\n    on: {push: }\n    script: make\n")
	scheduled, err := f.r.Scheduled(ctx)
	if err != nil || len(scheduled) != 1 || scheduled[0].RepoID != repo || len(scheduled[0].Jobs) != 1 || scheduled[0].Jobs[0].Name != "nightly" {
		t.Fatalf("Scheduled() = %+v, %v, want the website's nightly", scheduled, err)
	}
	slot := time.Date(2026, 11, 2, 3, 0, 0, 0, time.UTC)
	for i, want := range []bool{true, false} {
		if queued, err := f.r.QueueSlot(ctx, repo, "nightly", slot); err != nil || queued != want {
			t.Errorf("QueueSlot() #%d = %t, %v, want %t", i+1, queued, err, want)
		}
	}
	if queued, err := f.r.QueueSlot(ctx, uuid.New(), "nightly", slot); err != nil || queued {
		t.Errorf("QueueSlot(a repository that isn't there) = %t, %v, want nothing", queued, err)
	}
	want := []string{"#1 build main@a queued", "#2 nightly main@a queued"}
	if got := f.runs(t, repo); !slices.Equal(got, want) {
		t.Errorf("runs = %q, want %q", got, want)
	}
	if r := f.runOf(ctx, t, 2); r.Trigger.Kind != jobs.EventSchedule || !r.Trigger.Slot.Equal(slot) {
		t.Errorf("trigger = %+v, want the 03:00 slot", r.Trigger)
	}
	if n, err := f.r.DropSlots(ctx, slot.Add(time.Minute)); err != nil || n != 1 {
		t.Errorf("DropSlots() = %d, %v, want 1", n, err)
	}
}
