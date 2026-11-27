package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/runner"
)

// agentJobsFile declares website's checker and job ai/check, a task of checker's on every push.
const agentJobsFile = `agents:
  checker:
    permissions: [repo.read, annotations.write]
jobs:
  ai/check:
    on: {push: }
    agent:
      name: checker
      instructions: Check the links.
    permissions: [repo.read, annotations.write, repo.push]
`

// agentRun pushes main at sha with agentJobsFile and claims ai/check's run.
func (f *runFixture) agentRun(t *testing.T, old, sha string) runner.Run {
	t.Helper()
	f.push(t, pushOf(f.website.ID, f.olivia, "pack-"+sha[:1], []git.RefUpdate{{Name: "refs/heads/main", Old: old, New: sha}},
		commit(sha, person("Olivia", "olivia@example.com"))), tip(t, "main", "blob-"+sha[:1], "repository:\n  jobs:\n    ceiling: Developer\n"+agentJobsFile))
	run := f.claim(t)
	if run.Job != "ai/check" || run.Definition.Kind() != jobs.KindAgent {
		t.Fatalf("Claim() = %+v, want ai/check's run", run)
	}
	return run
}

// runTask finds the task of the run numbered run, which must be there.
func (f *runFixture) runTask(t *testing.T, run int64) agents.Task {
	t.Helper()
	tasks, err := NewAgentStore(f.pool).Tasks(context.Background(), f.acme.ID, agents.TaskFilter{RepoID: f.website.ID},
		[]uuid.UUID{f.website.ID}, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(tasks, func(t agents.Task) bool { return t.RunNumber == run })
	if i < 0 {
		t.Fatalf("Tasks() = %+v, want run %d's", tasks, run)
	}
	return tasks[i]
}

func TestRunStoreFindsJobAgents(t *testing.T) {
	t.Parallel()
	f := newRunFixture(t)
	f.agentRun(t, git.ZeroSHA, shaA)
	ctx := context.Background()
	tests := []struct {
		name, agent    string
		wantRepository bool
		wantErr        error
	}{
		{name: "the repository's", agent: "checker", wantRepository: true},
		{name: "in another case", agent: "Checker", wantRepository: true},
		{name: "the organization's", agent: agents.ReviewerName},
		{name: "none", agent: "nobody", wantErr: runner.ErrNoAgent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.r.JobAgent(ctx, f.website.ID, tt.agent)
			if !errors.Is(err, tt.wantErr) || (err == nil && (got.ID == uuid.Nil || got.Repository != tt.wantRepository)) {
				t.Errorf("JobAgent(%q) = %+v, %v, want repository %t, error %v", tt.agent, got, err, tt.wantRepository, tt.wantErr)
			}
		})
	}
	if _, err := f.r.JobAgent(ctx, f.secret.ID, "checker"); !errors.Is(err, runner.ErrNoAgent) {
		t.Errorf("JobAgent() of another repository's agent: error = %v, want ErrNoAgent", err)
	}
}

func TestRunStoreRunsAnAgentsTask(t *testing.T) {
	t.Parallel()
	f := newRunFixture(t)
	ctx := context.Background()
	run := f.agentRun(t, git.ZeroSHA, shaA)
	checker, err := f.r.JobAgent(ctx, f.website.ID, "checker")
	if err != nil {
		t.Fatal(err)
	}
	in := runner.RunTask{AgentID: checker.ID, Title: "Run 1 of ai/check at aaaaaaaaaaaa", Instructions: "Check the links.", Branch: "main"}
	if started, err := f.r.StartTask(ctx, run, in); !started || err != nil {
		t.Fatalf("StartTask() = %t, %v", started, err)
	}
	if started, err := f.r.StartTask(ctx, run, in); started || err != nil {
		t.Errorf("StartTask() of a running run = %t, %v, want false", started, err)
	}
	if got := f.note(t, f.website.ID, shaA, "ai/check"); got != "in_progress: Running by ai/check" {
		t.Errorf("annotation of a running agent's run = %q", got)
	}
	task := f.runTask(t, run.Number)
	if task.Kind != agents.KindJob || task.SHA != shaA || task.Branch != "main" || task.RunID != run.ID || task.AssignedBy == nil ||
		task.AssignedBy.ID != run.JobID || task.Agent.ID != checker.ID || task.State != agents.TaskQueued || task.Instructions != in.Instructions {
		t.Errorf("run's task = %+v", task)
	}
	if running, err := f.r.Running(ctx); err != nil || len(running) != 0 {
		t.Errorf("Running() = %+v, %v, want no pod's run", running, err)
	}
	f.checkRunGrant(t, task, run)
	if runs, err := f.r.AgentRuns(ctx); err != nil || len(runs) != 1 || runs[0].ID != run.ID || runs[0].Task != task.Number ||
		runs[0].State != agents.TaskQueued {
		t.Errorf("AgentRuns() = %+v, %v, want the run with its queued task", runs, err)
	}
	if err := f.r.CancelTask(ctx, run.ID, "The job's run timed out after 1h0m0s."); err != nil {
		t.Fatal(err)
	}
	if runs, err := f.r.AgentRuns(ctx); err != nil || len(runs) != 1 || runs[0].State != agents.TaskCancelled ||
		runs[0].Reason != "The job's run timed out after 1h0m0s." {
		t.Errorf("AgentRuns() after CancelTask() = %+v, %v", runs, err)
	}
	if n, err := f.r.CancelOrphanTasks(ctx); n != 0 || err != nil {
		t.Errorf("CancelOrphanTasks() with no orphan = %d, %v", n, err)
	}
}

// checkRunGrant checks that a token of the task caps it by what its run may do while it runs, and no more after.
func (f *runFixture) checkRunGrant(t *testing.T, task agents.Task, run runner.Run) {
	t.Helper()
	ctx := context.Background()
	secret := auth.NewSecret(auth.PrefixTaskToken)
	if err := f.auths.CreateTaskToken(ctx, task.ID, auth.Hash(secret), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	cred, _, err := f.auths.TaskToken(ctx, auth.Hash(secret))
	want := auth.RunGrant{RepoID: f.website.ID, Permissions: []string{"annotations.write", "repo.push", "repo.read"}}
	if err != nil || cred.Task.Run.RepoID != want.RepoID || !slices.Equal(cred.Task.Run.Permissions, want.Permissions) ||
		cred.Task.AssignerID != run.JobID {
		t.Errorf("TaskToken() = %+v, %v, want the run's grant %+v", cred.Task, err, want)
	}
}

func TestRunStoreCancelsTheTasksOfEndedRuns(t *testing.T) {
	t.Parallel()
	f := newRunFixture(t)
	ctx := context.Background()
	run := f.agentRun(t, git.ZeroSHA, shaA)
	checker, err := f.r.JobAgent(ctx, f.website.ID, "checker")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.StartTask(ctx, run, runner.RunTask{AgentID: checker.ID, Title: "Run 1", Branch: "gone"}); err != nil {
		t.Fatal(err)
	}
	if task := f.runTask(t, run.Number); task.BranchID != uuid.Nil {
		t.Errorf("task of a run on a branch that isn't there works on %s, want none", task.Branch)
	}
	if _, err := f.r.Finish(ctx, run.ID, jobs.StateCancelled, "cancelled by Olivia"); err != nil {
		t.Fatal(err)
	}
	if n, err := f.r.CancelOrphanTasks(ctx); n != 1 || err != nil {
		t.Fatalf("CancelOrphanTasks() = %d, %v, want 1", n, err)
	}
	task := f.runTask(t, run.Number)
	if task.State != agents.TaskCancelled || task.Reason != "Its run was cancelled." || task.CancelledBy == nil || task.CancelledBy.ID != run.JobID {
		t.Errorf("task of a cancelled run = %+v", task)
	}
	if n, err := f.r.CancelOrphanTasks(ctx); n != 0 || err != nil {
		t.Errorf("CancelOrphanTasks() again = %d, %v, want 0", n, err)
	}
}
