package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/runner"
)

// runFixture is jobsFixture with the controller's store and the credentials' store, which reads run tokens.
type runFixture struct {
	*jobsFixture
	r     *RunStore
	auths *AuthStore
}

func newRunFixture(t *testing.T) *runFixture {
	t.Helper()
	f := &runFixture{jobsFixture: newJobsFixture(t)}
	f.r, f.auths = NewRunStore(f.pool), NewAuthStore(f.pool)
	return f
}

// claim claims the queued run that waited longest, which must be there.
func (f *runFixture) claim(t *testing.T) runner.Run {
	t.Helper()
	run, ok, err := f.r.Claim(context.Background(), time.Now().Add(time.Hour))
	if err != nil || !ok {
		t.Fatalf("Claim() = %t, %v, want a run", ok, err)
	}
	return run
}

// token records a token for run that expires at expiresAt, and answers its hash.
func (f *runFixture) token(t *testing.T, runID uuid.UUID, expiresAt time.Time) []byte {
	t.Helper()
	secret := auth.NewSecret(auth.PrefixRunToken)
	if err := f.r.Token(context.Background(), runID, auth.Hash(secret), auth.Hint(secret), expiresAt); err != nil {
		t.Fatal(err)
	}
	return auth.Hash(secret)
}

func TestRunStoreRunsARun(t *testing.T) {
	t.Parallel()
	f := newRunFixture(t)
	ctx, repo := context.Background(), f.website.ID
	f.push(t, pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}},
		commit(shaA, person("Olivia", "olivia@example.com"))), tip(t, "main", "blob-1", jobsFile))
	run := f.claim(t)
	if run.Number != 1 || run.Job != "ci/build" || run.OrgSlug != "acme" || run.RepoSlug != "website" || run.SHA != shaA ||
		run.Ref != "refs/heads/main" || run.Definition.Script != "make" || run.OrgID != f.acme.ID || run.RepoID != repo {
		t.Fatalf("Claim() = %+v, want ci/build's run at main", run)
	}
	if _, ok, err := f.r.Claim(ctx, time.Now().Add(time.Hour)); ok || err != nil {
		t.Errorf("Claim() of a claimed run = %t, %v, want none", ok, err)
	}
	if err := f.r.Defer(ctx, run.ID, time.Now().Add(-time.Second), "waits for room in the organization's quota"); err != nil {
		t.Fatal(err)
	}
	if got := f.note(t, repo, shaA, "ci/build"); got != "pending: Waits for room in the organization's quota by ci/build" {
		t.Errorf("annotation of a run put off = %q", got)
	}
	if again := f.claim(t); again.ID != run.ID {
		t.Errorf("Claim() after the run's put off until now = %s, want %s", again.ID, run.ID)
	}
	hash := f.token(t, run.ID, time.Now().Add(time.Hour))
	f.checkToken(t, hash, run)
	if marked, err := f.r.MarkRunning(ctx, run.ID); !marked || err != nil {
		t.Fatalf("MarkRunning() = %t, %v", marked, err)
	}
	if got := f.note(t, repo, shaA, "ci/build"); got != "in_progress: Running by ci/build" {
		t.Errorf("annotation of a running run = %q", got)
	}
	if marked, err := f.r.MarkRunning(ctx, run.ID); marked || err != nil {
		t.Errorf("MarkRunning() of a running run = %t, %v, want false", marked, err)
	}
	f.checkToken(t, hash, run)
	f.checkFinish(t, run, hash)
}

// checkToken checks that hash's token acts as run's job, on its repository, with what its job declared.
func (f *runFixture) checkToken(t *testing.T, hash []byte, run runner.Run) {
	t.Helper()
	cred, principal, err := f.auths.Credential(context.Background(), hash)
	if err != nil || cred.Kind != auth.CredentialRunToken || cred.ID != run.ID || cred.PrincipalID != run.JobID || cred.OrgID != run.OrgID ||
		cred.RepoID != run.RepoID || !slices.Equal(cred.Permissions, run.Definition.Permissions) || principal.Kind != auth.KindJob ||
		principal.DisplayName != run.Job {
		t.Fatalf("Credential() = %+v, %+v, %v, want run %s's job", cred, principal, err, run.ID)
	}
	if err := f.auths.TouchCredential(context.Background(), cred.ID, cred.Kind, now); err != nil {
		t.Error(err)
	}
}

// checkFinish ends the running run, and checks it ended with its annotation, its token and the runs that run.
func (f *runFixture) checkFinish(t *testing.T, run runner.Run, hash []byte) {
	t.Helper()
	ctx := context.Background()
	if running, err := f.r.Running(ctx); err != nil || len(running) != 1 || running[0].ID != run.ID || running[0].StartedAt.IsZero() {
		t.Errorf("Running() = %+v, %v, want the run", running, err)
	}
	if finished, err := f.r.Finish(ctx, run.ID, jobs.StateSucceeded, "succeeded"); !finished || err != nil {
		t.Fatalf("Finish() = %t, %v", finished, err)
	}
	if got := f.note(t, f.website.ID, shaA, "ci/build"); got != "successful: Succeeded by ci/build" {
		t.Errorf("annotation of a run that succeeded = %q", got)
	}
	if _, _, err := f.auths.Credential(ctx, hash); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("Credential() of an ended run's token: error = %v, want not found", err)
	}
	if finished, err := f.r.Finish(ctx, run.ID, jobs.StateFailed, "its pod vanished"); finished || err != nil {
		t.Errorf("Finish() of an ended run = %t, %v, want false", finished, err)
	}
	if running, err := f.r.Running(ctx); err != nil || len(running) != 0 {
		t.Errorf("Running() = %+v, %v, want none", running, err)
	}
	standing, err := f.r.Runs(ctx, []uuid.UUID{run.ID, uuid.New()})
	if got := standing[run.ID]; err != nil || len(standing) != 1 || got.RepoID != f.website.ID || got.State != jobs.StateSucceeded || got.EndedAt == nil {
		t.Errorf("Runs() = %+v, %v, want the run, succeeded and ended", standing, err)
	}
	if orgs, err := f.r.Organizations(ctx, []uuid.UUID{f.acme.ID, uuid.New()}); err != nil || !slices.Equal(orgs, []uuid.UUID{f.acme.ID}) {
		t.Errorf("Organizations() = %v, %v, want Acme", orgs, err)
	}
}

func TestRunStoreKeepsSteps(t *testing.T) {
	t.Parallel()
	f := newRunFixture(t)
	ctx, repo := context.Background(), f.website.ID
	f.push(t, pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}},
		commit(shaA, person("Olivia", "olivia@example.com"))), tip(t, "main", "blob-1", jobsFile))
	run := f.claim(t)
	if steps, err := f.c.RunSteps(ctx, run.ID); err != nil || len(steps) != 0 {
		t.Errorf("RunSteps() before any were kept = %+v, %v, want none", steps, err)
	}
	started, ended := now.Add(-time.Minute), now
	kept := []jobs.Step{{Name: "cobranch-checkout", State: jobs.StepSucceeded, StartedAt: &started, EndedAt: &ended},
		{Name: "main", State: jobs.StepFailed, ExitCode: 137, Reason: "OOMKilled", StartedAt: &ended, EndedAt: &ended},
		{Name: "notify", State: jobs.StepWaiting}}
	if err := f.r.KeepSteps(ctx, run.ID, kept[:1]); err != nil {
		t.Fatal(err)
	}
	if err := f.r.KeepSteps(ctx, run.ID, kept); err != nil {
		t.Fatalf("KeepSteps() again = %v, want it to replace the steps kept before", err)
	}
	steps, err := f.c.RunSteps(ctx, run.ID)
	same := func(a, b jobs.Step) bool {
		at := func(t *time.Time) int64 {
			if t == nil {
				return 0
			}
			return t.UnixMicro()
		}
		return a.Name == b.Name && a.State == b.State && a.ExitCode == b.ExitCode && a.Reason == b.Reason &&
			at(a.StartedAt) == at(b.StartedAt) && at(a.EndedAt) == at(b.EndedAt)
	}
	if err != nil || !slices.EqualFunc(steps, kept, same) {
		t.Errorf("RunSteps() = %+v, %v, want %+v", steps, err, kept)
	}
}

func TestRunStoreDropsTokens(t *testing.T) {
	t.Parallel()
	f := newRunFixture(t)
	ctx, repo := context.Background(), f.website.ID
	f.push(t, pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}},
		commit(shaA, person("Olivia", "olivia@example.com"))), tip(t, "main", "blob-1", jobsFile))
	f.push(t, pushOf(repo, f.olivia, "pack-2", []git.RefUpdate{{Name: "refs/heads/next", Old: git.ZeroSHA, New: shaB}},
		commit(shaB, person("Olivia", "olivia@example.com"), shaA)), tip(t, "next", "blob-1", jobsFile))
	cancelled, expired := f.claim(t), f.claim(t)
	f.token(t, cancelled.ID, time.Now().Add(time.Hour))
	f.token(t, expired.ID, time.Now().Add(-time.Second))
	if _, err := f.c.CancelRun(ctx, f.olivia, repo, cancelled.Number); err != nil {
		t.Fatal(err)
	}
	if n, err := f.r.DropTokens(ctx); n != 2 || err != nil {
		t.Errorf("DropTokens() = %d, %v, want the cancelled run's and the expired one", n, err)
	}
	live := f.token(t, expired.ID, time.Now().Add(time.Hour))
	if n, err := f.r.DropTokens(ctx); n != 0 || err != nil {
		t.Errorf("DropTokens() = %d, %v, want none", n, err)
	}
	if _, _, err := f.auths.Credential(ctx, live); err != nil {
		t.Errorf("Credential() of a queued run's token = %v, want it", err)
	}
}

// hookFile declares a webhook job on every push.
const hookFile = `jobs:
  deploy:
    on: {push: }
    webhook: {url: "https://deploy.example.com/hook", signingSecret: 0b9e5a3c-7d1f-4e2a-9c8b-5f6d7e8a9b0c}
`

func TestRunStoreEndsWebhookRunsTheirReceiversReport(t *testing.T) {
	t.Parallel()
	f := newRunFixture(t)
	ctx, repo := context.Background(), f.website.ID
	f.push(t, pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}},
		commit(shaA, person("Olivia", "olivia@example.com"))), tip(t, "main", "blob-1", hookFile))
	run := f.claim(t)
	if run.Definition.Kind() != jobs.KindWebhook || run.Job != "deploy" || run.QueuedAt.IsZero() {
		t.Fatalf("Claim() = %+v, want deploy's webhook run, with when it was queued", run)
	}
	hash := f.token(t, run.ID, time.Now().Add(time.Hour))
	if marked, err := f.r.MarkRunning(ctx, run.ID); !marked || err != nil {
		t.Fatalf("MarkRunning() = %t, %v", marked, err)
	}
	if running, err := f.r.Running(ctx); err != nil || len(running) != 0 {
		t.Errorf("Running() = %+v, %v, want no pod's run", running, err)
	}
	report := func(status code.AnnotationStatus) {
		t.Helper()
		in := code.AnnotationInput{Name: "deploy", Status: status, Summary: "Deploying", Version: code.AnyVersion}
		if _, err := f.c.SetAnnotation(ctx, run.JobID, repo, shaA, in); err != nil {
			t.Fatal(err)
		}
	}
	report(code.AnnotationInProgress)
	if running, err := f.r.RunningWebhooks(ctx); err != nil || len(running) != 1 || running[0].ID != run.ID || running[0].StartedAt.IsZero() {
		t.Errorf("RunningWebhooks() after In progress = %+v, %v, want the run", running, err)
	}
	report(code.AnnotationSuccessful)
	if got, want := f.runs(t, repo), []string{"#1 deploy main@a succeeded: its webhook's receiver reported successful"}; !slices.Equal(got, want) {
		t.Errorf("runs = %q, want %q", got, want)
	}
	if got := f.note(t, repo, shaA, "deploy"); got != "successful: Deploying by deploy" {
		t.Errorf("annotation = %q, want the receiver's", got)
	}
	if _, _, err := f.auths.Credential(ctx, hash); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("Credential() of the reported run's token: error = %v, want not found", err)
	}
	if running, err := f.r.RunningWebhooks(ctx); err != nil || len(running) != 0 {
		t.Errorf("RunningWebhooks() = %+v, %v, want none", running, err)
	}
}

// TestRunStoreEndsAWebhookRunReportedBeforeItRuns has a receiver report before its post's 2xx reaches the controller,
// while the run is still queued: the report ends the run, which then names it as the cause of the runs it triggers,
// and the controller's mark comes too late to undo it.
func TestRunStoreEndsAWebhookRunReportedBeforeItRuns(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status code.AnnotationStatus
		want   string
	}{
		{"Successful", code.AnnotationSuccessful, "#1 deploy main@a succeeded: its webhook's receiver reported successful"},
		{"Failed", code.AnnotationFailed, "#1 deploy main@a failed: its webhook's receiver reported failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkEarlyReport(t, tt.status, tt.want)
		})
	}
}

// checkEarlyReport has deploy's receiver report status while its run is queued, and checks that the run ended so, want
// being its line, that the report named it as the cause of what it triggered, and that the controller's mark after
// changed nothing.
func checkEarlyReport(t *testing.T, status code.AnnotationStatus, want string) {
	t.Helper()
	f := newRunFixture(t)
	ctx, repo := context.Background(), f.website.ID
	f.push(t, pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}},
		commit(shaA, person("Olivia", "olivia@example.com"))), tip(t, "main", "blob-1", hookFile+followerOfDeploy))
	run := f.claim(t)
	hash := f.token(t, run.ID, time.Now().Add(time.Hour))
	in := code.AnnotationInput{Name: "deploy", Status: status, Summary: "Deployed", Version: code.AnyVersion}
	if _, err := f.c.SetAnnotation(ctx, run.JobID, repo, shaA, in); err != nil {
		t.Fatal(err)
	}
	if marked, err := f.r.MarkRunning(ctx, run.ID); marked || err != nil {
		t.Errorf("MarkRunning() after the report = %t, %v, want the run ended", marked, err)
	}
	got := f.runs(t, repo)
	if len(got) < 1 || got[0] != want {
		t.Errorf("runs = %q, want %q first", got, want)
	}
	if got := f.note(t, repo, shaA, "deploy"); got != string(status)+": Deployed by deploy" {
		t.Errorf("annotation = %q, want the receiver's", got)
	}
	if _, _, err := f.auths.Credential(ctx, hash); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("Credential() of the reported run's token: error = %v, want not found", err)
	}
	ended, err := f.c.Run(ctx, repo, run.Number)
	if err != nil || ended.StartedAt == nil {
		t.Errorf("Run() = %+v, %v, want it started when it was reported", ended, err)
	}
	if status == code.AnnotationSuccessful {
		if smoke, err := f.c.Run(ctx, repo, run.Number+1); err != nil || smoke.CauseRun != run.Number {
			t.Errorf("Run() of the run the report triggered = %+v, %v, want #%d its cause", smoke, err, run.Number)
		}
	}
}

// followerOfDeploy declares a job that runs once deploy succeeds.
const followerOfDeploy = `  smoke:
    on: {annotation: {names: [deploy], statuses: [successful]}}
    script: make smoke
`

func TestRunStoreEndsNoPodRunItsAnnotationReports(t *testing.T) {
	t.Parallel()
	f := newRunFixture(t)
	ctx, repo := context.Background(), f.website.ID
	f.push(t, pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}},
		commit(shaA, person("Olivia", "olivia@example.com"))), tip(t, "main", "blob-1", jobsFile))
	run := f.claim(t)
	if marked, err := f.r.MarkRunning(ctx, run.ID); !marked || err != nil {
		t.Fatalf("MarkRunning() = %t, %v", marked, err)
	}
	in := code.AnnotationInput{Name: "ci/build", Status: code.AnnotationSuccessful, Version: code.AnyVersion}
	if _, err := f.c.SetAnnotation(ctx, run.JobID, repo, shaA, in); err != nil {
		t.Fatal(err)
	}
	if running, err := f.r.Running(ctx); err != nil || len(running) != 1 || running[0].ID != run.ID {
		t.Errorf("Running() = %+v, %v, want the script's run, which its pod ends", running, err)
	}
}
