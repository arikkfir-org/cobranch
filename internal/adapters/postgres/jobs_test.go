package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/repos"
)

// jobsFile declares a job on every push, one on tags, and one on pushes to docs/ branches.
const jobsFile = `jobs:
  ci/build:
    on: {push: }
    script: make
  release:
    on: {tag: {tags: ["v*"]}}
    script: make release
  docs:
    on: {push: {branches: [docs/*]}}
    script: make docs
`

// tip is the /cobranch.yaml of content at branch, recorded by blob, as the Git server works it out.
func tip(t *testing.T, branch, blob, content string) git.TipSettings {
	t.Helper()
	settings, err := code.ParseSettings([]byte(content))
	if err != nil {
		return git.TipSettings{Branch: branch, Blob: blob, Error: err.Error()}
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	return git.TipSettings{Branch: branch, Blob: blob, Settings: encoded}
}

// jobsFixture is gitFixture with the code store, which reads runs.
type jobsFixture struct {
	*gitFixture
	c *CodeStore
}

func newJobsFixture(t *testing.T) *jobsFixture {
	t.Helper()
	f := &jobsFixture{gitFixture: newGitFixture(t)}
	f.c = NewCodeStore(f.pool)
	return f
}

// push records p, with the settings at its branches' tips, and has the push worker process it.
func (f *jobsFixture) push(t *testing.T, p git.Push, settings ...git.TipSettings) {
	t.Helper()
	p.Settings = settings
	if err := f.g.Record(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	f.process(t)
}

// runs lists the repository's runs, oldest first, as "#n job ref@sha state: reason".
func (f *jobsFixture) runs(t *testing.T, repoID uuid.UUID) []string {
	t.Helper()
	list, err := f.c.Runs(context.Background(), repoID, "", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range slices.Backward(list) {
		ref := strings.TrimPrefix(strings.TrimPrefix(r.Ref, "refs/heads/"), "refs/")
		line := fmt.Sprintf("#%d %s %s@%s %s", r.Number, r.Job.DisplayName, ref, r.SHA[:1], r.State)
		if r.Reason != "" {
			line += ": " + r.Reason
		}
		out = append(out, line)
	}
	return out
}

// note is the annotation of name on the repository's commit, as "status: summary by principal".
func (f *jobsFixture) note(t *testing.T, repoID uuid.UUID, sha, name string) string {
	t.Helper()
	list, err := f.c.Annotations(context.Background(), repoID, sha)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range list {
		if a.Name == name {
			by := "nobody"
			if a.Principal != nil {
				by = a.Principal.DisplayName
			}
			return fmt.Sprintf("%s: %s by %s", a.Status, a.Summary, by)
		}
	}
	return "none"
}

func TestGitStoreQueuesRuns(t *testing.T) {
	f := newJobsFixture(t)
	repo := f.website.ID
	shaD, shaE := strings.Repeat("d", 40), strings.Repeat("e", 40)
	settings := func(branch string) git.TipSettings { return tip(t, branch, "blob-1", jobsFile) }
	first := []string{"#1 ci/build main@a queued", "#2 release tags/v1@a queued"}
	second := []string{"#1 ci/build main@a cancelled: superseded by #3", "#2 release tags/v1@a queued", "#3 ci/build main@b queued"}
	third := append(slices.Clone(second), "#4 ci/build feature@c waiting: waits for a member to start it")
	fourth := append(slices.Clone(second), "#4 ci/build feature@c cancelled: superseded by #5", "#5 ci/build docs/new@c queued", "#6 docs docs/new@c queued")
	steps := []struct {
		name     string
		push     git.Push
		settings []git.TipSettings
		want     []string
	}{
		{name: "a member creates main and tags it", want: first,
			push: withPeeled(pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA},
				{Name: "refs/tags/v1", Old: git.ZeroSHA, New: shaC}}, commit(shaA, person("Olivia", "olivia@example.com"))), "refs/tags/v1", shaA),
			settings: []git.TipSettings{settings("main")}},
		{name: "a member moves main on", want: second,
			push:     pushOf(repo, f.olivia, "pack-2", []git.RefUpdate{{Name: "refs/heads/main", Old: shaA, New: shaB}}, commit(shaB, person("Olivia", "olivia@example.com"), shaA)),
			settings: []git.TipSettings{settings("main")}},
		{name: "an outsider pushes a branch", want: third,
			push:     pushOf(repo, f.nia, "pack-3", []git.RefUpdate{{Name: "refs/heads/feature", Old: git.ZeroSHA, New: shaC}}, commit(shaC, person("Nia", "nia@example.com"), shaB)),
			settings: []git.TipSettings{settings("feature")}},
		{name: "a member pushes the same commit", want: fourth,
			push:     pushOf(repo, f.max, "", []git.RefUpdate{{Name: "refs/heads/docs/new", Old: git.ZeroSHA, New: shaC}}),
			settings: []git.TipSettings{settings("docs/new")}},
		{name: "a commit with a run already", want: fourth,
			push:     pushOf(repo, f.max, "", []git.RefUpdate{{Name: "refs/heads/again", Old: git.ZeroSHA, New: shaB}}),
			settings: []git.TipSettings{settings("again")}},
		{name: "a branch whose file doesn't parse", want: fourth,
			push:     pushOf(repo, f.max, "pack-4", []git.RefUpdate{{Name: "refs/heads/broken", Old: git.ZeroSHA, New: shaD}}, commit(shaD, person("Max", "max@example.com"), shaB)),
			settings: []git.TipSettings{tip(t, "broken", "blob-2", "jobs: [x]\n")}},
		{name: "a branch without the file", want: fourth,
			push:     pushOf(repo, f.max, "pack-5", []git.RefUpdate{{Name: "refs/heads/bare", Old: git.ZeroSHA, New: shaE}}, commit(shaE, person("Max", "max@example.com"), shaB)),
			settings: []git.TipSettings{{Branch: "bare"}}},
	}
	for _, step := range steps {
		f.push(t, step.push, step.settings...)
		if got := f.runs(t, repo); !slices.Equal(got, step.want) {
			t.Fatalf("%s: runs = %q, want %q", step.name, got, step.want)
		}
	}
	f.checkQueued(t)
}

// checkQueued checks what TestGitStoreQueuesRuns's pushes left beside the runs: the jobs' annotations, their
// principals, and the recorded files.
func (f *jobsFixture) checkQueued(t *testing.T) {
	t.Helper()
	repo, shaD := f.website.ID, strings.Repeat("d", 40)
	for _, check := range []struct{ sha, name, want string }{
		{shaA, "ci/build", "failed: Superseded by #3 by ci/build"},
		{shaA, "release", "pending: Queued by release"},
		{shaB, "ci/build", "pending: Queued by ci/build"},
		{shaC, "ci/build", "pending: Queued by ci/build"},
		{shaD, "ci/build", "none"},
	} {
		if got := f.note(t, repo, check.sha, check.name); got != check.want {
			t.Errorf("annotation %s at %s = %q, want %q", check.name, check.sha[:1], got, check.want)
		}
	}
	if n := count(t, f.pool, "SELECT count(*) FROM principals p JOIN jobs j ON j.principal_id = p.id WHERE p.kind = 'job' AND p.org_id = $1", f.acme.ID); n != 3 {
		t.Errorf("%d job principals, want ci/build, release and docs", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM settings_files WHERE repo_id = $1 AND error <> ''", repo); n != 1 {
		t.Errorf("%d recorded files that don't parse, want broken's", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM branches WHERE repo_id = $1 AND settings_blob IS NULL AND deleted_at IS NULL", repo); n != 1 {
		t.Errorf("%d branches without a file, want bare", n)
	}
}

// withPeeled is p, its tag ref naming commit sha.
func withPeeled(p git.Push, ref, sha string) git.Push {
	p.Peeled = map[string]string{ref: sha}
	return p
}

func TestGitStoreRunsCeiling(t *testing.T) {
	f := newJobsFixture(t)
	repo := f.website.ID
	file := "repository:\n  outsidePushes: run\njobs:\n  pusher:\n    on: {push: }\n    script: git push\n    permissions: [repo.push]\n"
	f.push(t, pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}}, commit(shaA, person("Olivia", "olivia@example.com"))),
		tip(t, "main", "blob-1", file))
	f.push(t, pushOf(repo, f.nia, "pack-2", []git.RefUpdate{{Name: "refs/heads/outside", Old: git.ZeroSHA, New: shaB}}, commit(shaB, person("Nia", "nia@example.com"), shaA)),
		tip(t, "outside", "blob-2", strings.ReplaceAll(file, "repo.push", "repo.read")))
	want := []string{"#1 pusher main@a failed: the job lists repo.push, beyond its ceiling, Automation", "#2 pusher outside@b queued"}
	if got := f.runs(t, repo); !slices.Equal(got, want) {
		t.Errorf("runs = %q, want %q", got, want)
	}
	if got := f.note(t, repo, shaA, "pusher"); got != "failed: The job lists repo.push, beyond its ceiling, Automation by pusher" {
		t.Errorf("annotation = %q", got)
	}
}

func TestGitStoreRunsTakenAnnotation(t *testing.T) {
	f := newJobsFixture(t)
	repo := f.website.ID
	f.push(t, pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}}, commit(shaA, person("Olivia", "olivia@example.com"))))
	if _, err := f.c.SetAnnotation(context.Background(), f.max, repo, shaA, code.AnnotationInput{Name: "ci/build", Status: code.AnnotationSuccessful}); err != nil {
		t.Fatal(err)
	}
	f.push(t, pushOf(repo, f.olivia, "", []git.RefUpdate{{Name: "refs/heads/feature", Old: git.ZeroSHA, New: shaA}}), tip(t, "feature", "blob-1", jobsFile))
	runs := f.runs(t, repo)
	if len(runs) != 1 || !strings.HasPrefix(runs[0], "#1 ci/build feature@a failed: another principal, ") || !strings.HasSuffix(runs[0], ", holds annotation ci/build on this commit") {
		t.Errorf("runs = %q, want ci/build failed for Max's annotation", runs)
	}
}

// pushTwoRuns has Olivia push main, queueing run #1 of ci/build, then Nia, an outsider, push feature, whose run #2
// waits for a member.
func (f *jobsFixture) pushTwoRuns(t *testing.T) {
	t.Helper()
	repo := f.website.ID
	f.push(t, pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}}, commit(shaA, person("Olivia", "olivia@example.com"))),
		tip(t, "main", "blob-1", jobsFile))
	f.push(t, pushOf(repo, f.nia, "pack-2", []git.RefUpdate{{Name: "refs/heads/feature", Old: git.ZeroSHA, New: shaB}}, commit(shaB, person("Nia", "nia@example.com"), shaA)),
		tip(t, "feature", "blob-1", jobsFile))
}

func TestCodeStoreStartAndCancelRuns(t *testing.T) {
	f := newJobsFixture(t)
	ctx := context.Background()
	repo := f.website.ID
	f.pushTwoRuns(t)

	if _, err := f.c.StartRun(ctx, f.olivia, repo, 1); !errors.Is(err, code.ErrRunNotWaiting) {
		t.Errorf("StartRun(a queued run) = %v, want %v", err, code.ErrRunNotWaiting)
	}
	started, err := f.c.StartRun(ctx, f.olivia, repo, 2)
	if err != nil || started.State != jobs.StateQueued || started.StartedBy == nil || started.StartedBy.ID != f.olivia || started.Version != 2 {
		t.Fatalf("StartRun() = %+v, %v; want it queued for Olivia", started, err)
	}
	if got := f.note(t, repo, shaB, "ci/build"); got != "pending: Queued by ci/build" {
		t.Errorf("a started run's annotation = %q", got)
	}
	cancelled, err := f.c.CancelRun(ctx, f.max, repo, 2)
	if err != nil || cancelled.State != jobs.StateCancelled || !strings.HasPrefix(cancelled.Reason, "cancelled by ") || cancelled.EndedAt == nil {
		t.Fatalf("CancelRun() = %+v, %v", cancelled, err)
	}
	if got := f.note(t, repo, shaB, "ci/build"); !strings.HasPrefix(got, "failed: Cancelled by ") {
		t.Errorf("a cancelled run's annotation = %q", got)
	}
	if _, err := f.c.CancelRun(ctx, f.max, repo, 2); !errors.Is(err, code.ErrRunEnded) {
		t.Errorf("CancelRun(again) = %v, want %v", err, code.ErrRunEnded)
	}
	for kind, actor := range map[string]uuid.UUID{"run.started": f.olivia, "run.cancelled": f.max} {
		if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind = $1 AND actor_id = $2", kind, actor); n != 1 {
			t.Errorf("%d %s events, want 1", n, kind)
		}
	}
}

func TestCodeStoreReadsRuns(t *testing.T) {
	f := newJobsFixture(t)
	ctx := context.Background()
	repo := f.website.ID
	f.pushTwoRuns(t)
	for _, number := range []int64{3, 0} {
		if _, err := f.c.CancelRun(ctx, f.max, repo, number); !errors.Is(err, repos.ErrNotFound) {
			t.Errorf("CancelRun(#%d) = %v, want %v", number, err, repos.ErrNotFound)
		}
		if _, err := f.c.Run(ctx, repo, number); !errors.Is(err, repos.ErrNotFound) {
			t.Errorf("Run(#%d) = %v, want %v", number, err, repos.ErrNotFound)
		}
	}
	if _, err := f.c.Run(ctx, f.secret.ID, 1); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("another repository's Run(#1) = %v, want %v", err, repos.ErrNotFound)
	}
	run, err := f.c.Run(ctx, repo, 1)
	if err != nil || run.Trigger.Kind != jobs.EventPush || run.Trigger.ActorID != f.olivia || run.Definition.Script != "make" || run.Job.DisplayName != "ci/build" {
		t.Errorf("Run(#1) = %+v, %v", run, err)
	}
	page, err := f.c.Runs(ctx, repo, shaB, 0, 10)
	if err != nil || len(page) != 1 || page[0].Number != 2 {
		t.Errorf("Runs(at b) = %+v, %v", page, err)
	}
	if page, err = f.c.Runs(ctx, repo, "", 2, 10); err != nil || len(page) != 1 || page[0].Number != 1 {
		t.Errorf("Runs(before #2) = %+v, %v", page, err)
	}
}
