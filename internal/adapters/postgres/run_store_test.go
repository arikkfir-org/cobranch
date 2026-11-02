package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
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
	if err != nil || len(standing) != 1 || standing[run.ID] != (runner.Standing{RepoID: f.website.ID, State: jobs.StateSucceeded}) {
		t.Errorf("Runs() = %+v, %v, want the run, succeeded", standing, err)
	}
	if orgs, err := f.r.Organizations(ctx, []uuid.UUID{f.acme.ID, uuid.New()}); err != nil || !slices.Equal(orgs, []uuid.UUID{f.acme.ID}) {
		t.Errorf("Organizations() = %v, %v, want Acme", orgs, err)
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
