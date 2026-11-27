package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/runner"
)

// The runs of jobs that name agents: each is one task of its agent, of kind job, which the job assigns at the run's
// commit as the run starts, and which the controller follows to the run's end
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/repo-agents.md, "Jobs that name an
// agent").

// JobAgent finds the agent a job of the repository names: the repository's own unless it is retired, and otherwise the
// organization's (runner.Store).
func (s *RunStore) JobAgent(ctx context.Context, repoID uuid.UUID, name string) (runner.JobAgent, error) {
	row, err := s.db.Everywhere(controllerQueues).JobAgent(ctx, repoID, name)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return runner.JobAgent{}, runner.ErrNoAgent
	case err != nil:
		return runner.JobAgent{}, fmt.Errorf("finding agent %s: %w", name, err)
	}
	return runner.JobAgent{ID: row.PrincipalID, Name: row.Name, Repository: row.Repository}, nil
}

// StartTask marks a queued run running and assigns its agent the run's task, as its job, at its commit and on the
// branch it was pushed to while that branch lives; its job's annotation says In progress (runner.Store).
func (s *RunStore) StartTask(ctx context.Context, run runner.Run, t runner.RunTask) (bool, error) {
	started := false
	err := s.db.Everywhere(controllerQueues).Transact(ctx, "starting the run's task", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		if err := q.LockRunQueueOfRun(ctx, run.ID); err != nil {
			return fmt.Errorf("locking run %s's repository's runs: %w", run.ID, err)
		}
		row, err := q.MarkRunRunning(ctx, run.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("marking run %s running: %w", run.ID, err)
		}
		if err := insertRunTask(ctx, q, run, t); err != nil {
			return err
		}
		started = true
		return annotateRun(ctx, q, ranRun(row), annotationOfState(jobs.StateRunning, ""), true)
	})
	return started, err
}

// insertRunTask numbers the run's task in its organization and records it, Queued, assigned by the run's job.
func insertRunTask(ctx context.Context, q *queries.Queries, run runner.Run, t runner.RunTask) error {
	var branchID *uuid.UUID
	if t.Branch != "" {
		b, err := q.TaskBranch(ctx, run.RepoID, nil, t.Branch)
		switch {
		case err == nil:
			branchID = &b.ID
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("finding branch %s: %w", t.Branch, err)
		}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("making the task's ID: %w", err)
	}
	number, err := q.NextTaskNumber(ctx, run.OrgID)
	if err != nil {
		return fmt.Errorf("numbering run %s's task: %w", run.ID, err)
	}
	if err := q.InsertTask(ctx, queries.InsertTaskParams{ID: id, OrgID: run.OrgID, Number: number, AgentID: t.AgentID, Kind: agents.KindJob,
		AssignedBy: &run.JobID, Title: t.Title, Instructions: t.Instructions, RepoID: &run.RepoID, BranchID: branchID, Sha: &run.SHA,
		RunID: &run.ID}); err != nil {
		return fmt.Errorf("recording run %s's task: %w", run.ID, err)
	}
	details := map[string]any{"number": number, "agent": t.AgentID, "kind": agents.KindJob}
	return orgEvent(ctx, q, &run.OrgID, run.JobID, "task.assigned", id, details)
}

// AgentRuns lists the runs of jobs that name agents that run, with where their tasks stand (runner.Store).
func (s *RunStore) AgentRuns(ctx context.Context) ([]runner.AgentRun, error) {
	rows, err := s.db.Everywhere(controllerQueues).AgentRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing the running runs of agents: %w", err)
	}
	out := make([]runner.AgentRun, 0, len(rows))
	for _, r := range rows {
		var job jobs.Job
		if err := json.Unmarshal(r.Definition, &job); err != nil {
			return nil, fmt.Errorf("decoding run %s's definition: %w", r.ID, err)
		}
		a := runner.AgentRun{Running: runner.Running{ID: r.ID, OrgID: r.OrgID, RepoID: r.RepoID, Number: r.Number, StartedAt: orZero(r.StartedAt),
			Timeout: time.Duration(job.Timeout)}}
		if r.TaskNumber != nil {
			a.Task, a.State, a.Summary, a.Reason = *r.TaskNumber, agents.TaskState(orEmpty(r.TaskState)), orEmpty(r.TaskSummary), orEmpty(r.TaskReason)
		}
		out = append(out, a)
	}
	return out, nil
}

// CancelTask cancels the run's task unless it ended, as the run's job, for reason, and answers whether it did
// (runner.Store).
func (s *RunStore) CancelTask(ctx context.Context, runID uuid.UUID, reason string) (bool, error) {
	cancelled := false
	err := s.db.Everywhere(controllerQueues).Transact(ctx, "cancelling the run's task", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		row, err := q.CancelRunTask(ctx, reason, runID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("cancelling run %s's task: %w", runID, err)
		}
		cancelled = true
		return orgEvent(ctx, q, &row.OrgID, row.JobID, "task.cancelled", row.ID, map[string]any{"number": row.Number})
	})
	return cancelled, err //nolint:wrapcheck // Transact names what it was doing
}

// CancelOrphanTasks cancels the jobs' tasks whose runs ended without them, or are gone, as each one's job, and answers
// how many (runner.Store).
func (s *RunStore) CancelOrphanTasks(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.Everywhere(controllerQueues).Transact(ctx, "cancelling the tasks of ended runs", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		rows, err := q.CancelOrphanTasks(ctx)
		if err != nil {
			return fmt.Errorf("cancelling the tasks of ended runs: %w", err)
		}
		for _, t := range rows {
			if err := orgEvent(ctx, q, &t.OrgID, orNil(t.AssignedBy), "task.cancelled", t.ID, map[string]any{"number": t.Number}); err != nil {
				return err
			}
		}
		n = int64(len(rows))
		return nil
	})
	return n, err
}
