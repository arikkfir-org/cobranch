package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/repos"
)

// A job that names an agent runs as one of the agent's tasks, of kind job, at the run's commit, assigned by the job,
// and the run ends as the task ends
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/repo-agents.md, "Jobs that name an
// agent").

// ErrNoAgent is a name that neither the run's repository nor its organization has a working agent of.
var ErrNoAgent = errors.New("no agent of that name")

// JobAgent is the agent a job names, as its run finds it.
type JobAgent struct {
	// ID is the agent's principal.
	ID   uuid.UUID
	Name string
	// Repository is whether it is the run's repository's own, which works there; an organization's must read it.
	Repository bool
}

// RunTask is the task a run of a job that names an agent assigns it.
type RunTask struct {
	AgentID             uuid.UUID
	Title, Instructions string
	// Branch is the branch the run's commit was pushed to, without refs/heads/; empty for a tag's run.
	Branch string
}

// AgentRun is a running run of a job that names an agent, and where its task stands.
type AgentRun struct {
	Running
	// Task is its task's number, 0 once the task is gone.
	Task            int64
	State           agents.TaskState
	Summary, Reason string
}

// Authorizer checks a principal's permissions: auth.Authorizer.
type Authorizer interface {
	Can(ctx context.Context, principalID uuid.UUID, permission string, scope auth.Scope, scopeID uuid.UUID) error
}

// assign starts a claimed run of a job that names an agent: it finds the agent, the repository's first and then the
// organization's, which must read the repository, and assigns it the run's task as the job, marking the run running.
// A run whose agent isn't there, or may not read the repository, fails naming it.
func (s *Service) assign(ctx context.Context, run Run) error {
	name := run.Definition.Agent.Name
	agent, err := s.store.JobAgent(ctx, run.RepoID, name)
	switch {
	case errors.Is(err, ErrNoAgent):
		return s.fail(ctx, run.ID, fmt.Sprintf("neither repository %s nor its organization has an agent named %s", run.RepoSlug, name))
	case err != nil:
		return fmt.Errorf("finding run %s's agent %s: %w", run.ID, name, err)
	}
	if !agent.Repository {
		switch err := s.authz.Can(ctx, agent.ID, repos.PermRead, auth.ScopeRepository, run.RepoID); {
		case errors.Is(err, auth.ErrForbidden):
			return s.fail(ctx, run.ID, fmt.Sprintf("agent %s may not read repository %s: grant it %s there", agent.Name, run.RepoSlug, repos.PermRead))
		case err != nil:
			return fmt.Errorf("checking agent %s reads run %s's repository: %w", agent.Name, run.ID, err)
		}
	}
	branch, _ := strings.CutPrefix(run.Ref, "refs/heads/")
	if branch == run.Ref {
		branch = ""
	}
	t := RunTask{AgentID: agent.ID, Title: fmt.Sprintf("Run %d of %s at %s", run.Number, run.Job, short(run.SHA)),
		Instructions: run.Definition.Agent.Instructions, Branch: branch}
	if _, err := s.store.StartTask(ctx, run, t); err != nil {
		return fmt.Errorf("assigning run %s's task: %w", run.ID, err)
	}
	return nil
}

// followTasks ends the running runs of jobs that name agents as their tasks ended, Successful only for a task that
// succeeded, with the task's summary or reason; cancels the task of one that outlived its timeout, failing it; and
// cancels the tasks of runs that ended otherwise, which a member cancelled or a newer run superseded.
func (s *Service) followTasks(ctx context.Context) error {
	runs, err := s.store.AgentRuns(ctx)
	if err != nil {
		return fmt.Errorf("listing the running runs of agents: %w", err)
	}
	var errs []error
	for _, r := range runs {
		if err := s.followTask(ctx, r); err != nil {
			errs = append(errs, err)
		}
	}
	if _, err := s.store.CancelOrphanTasks(ctx); err != nil {
		errs = append(errs, fmt.Errorf("cancelling the tasks of ended runs: %w", err))
	}
	return errors.Join(errs...)
}

// followTask ends r as its task ended, or once it outlived its timeout, and leaves it running otherwise. A task that
// ended since AgentRuns read it ends its run on the next pass, as it ended, rather than as timed out.
func (s *Service) followTask(ctx context.Context, r AgentRun) error {
	state, reason := taskOutcome(r)
	if state == "" {
		timeout := s.timeout(jobs.Job{Timeout: jobs.Duration(r.Timeout)})
		if s.now().Sub(r.StartedAt) <= timeout {
			return nil
		}
		why := "timed out after " + timeout.String()
		cancelled, err := s.store.CancelTask(ctx, r.ID, "The job's run "+why+".")
		switch {
		case err != nil:
			return fmt.Errorf("cancelling run %s's task: %w", r.ID, err)
		case !cancelled:
			return nil
		}
		state, reason = jobs.StateFailed, why
	}
	if _, err := s.store.Finish(ctx, r.ID, state, reason); err != nil {
		return fmt.Errorf("ending run %s: %w", r.ID, err)
	}
	return nil
}

// taskOutcome is how a run ends as its task stands: Successful with the task's summary when it succeeded, Failed with
// its reason when it failed or was cancelled or is gone; no state while it works.
func taskOutcome(r AgentRun) (jobs.State, string) {
	switch {
	case r.Task == 0:
		return jobs.StateFailed, "its task is gone"
	case r.State == agents.TaskSucceeded:
		return jobs.StateSucceeded, summary(r.Summary, "Succeeded")
	case r.State == agents.TaskFailed:
		return jobs.StateFailed, summary(r.Reason, "Its task failed")
	case r.State == agents.TaskCancelled && strings.TrimSpace(r.Reason) == "":
		return jobs.StateFailed, "Its task was cancelled"
	case r.State == agents.TaskCancelled:
		return jobs.StateFailed, summary("Its task was cancelled: "+r.Reason, "")
	}
	return "", ""
}

// summary is text as a job's annotation shows it, at most code.MaxAnnotationSummary characters, or otherwise when
// text says nothing.
func summary(text, otherwise string) string {
	if text = strings.TrimSpace(text); text == "" {
		return otherwise
	}
	if utf8.RuneCountInString(text) <= code.MaxAnnotationSummary {
		return text
	}
	return string([]rune(text)[:code.MaxAnnotationSummary-1]) + "…"
}

// short is a commit's name as a reader recognizes it: its first 12 characters.
func short(sha string) string {
	return sha[:min(len(sha), 12)]
}
