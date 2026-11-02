package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/runner"
)

// RunStore keeps the runs the job controller runs (db/migrations/00021_jobs.sql) and their tokens
// (db/migrations/00027_run_tokens.sql): runner.Store on PostgreSQL. What it changes is Cobranch's own doing, which
// names nobody; each change of a run's state sets its job's annotation to match, as the job, in the same transaction.
type RunStore struct {
	pool *pgxpool.Pool
	q    *queries.Queries
}

// NewRunStore returns the runs' store on pool's database.
func NewRunStore(pool *pgxpool.Pool) *RunStore {
	return &RunStore{pool: pool, q: queries.New(pool)}
}

var _ runner.Store = (*RunStore)(nil)

// ranRun is a run whose state just changed: what its job's annotation needs.
type ranRun struct {
	ID, OrgID, RepoID, JobID uuid.UUID
	Sha, Job                 string
}

// Claim claims the queued run of a pod or a script that waited longest and that no controller holds, until leaseUntil
// (runner.Store). A run whose definition doesn't decode fails, so that it isn't claimed again and again.
func (s *RunStore) Claim(ctx context.Context, leaseUntil time.Time) (runner.Run, bool, error) {
	row, err := s.q.ClaimRun(ctx, leaseUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return runner.Run{}, false, nil
	}
	if err != nil {
		return runner.Run{}, false, fmt.Errorf("claiming a queued run: %w", err)
	}
	var job jobs.Job
	if err := json.Unmarshal(row.Definition, &job); err != nil {
		reason := "Cobranch can't read the job's definition: " + err.Error()
		if _, ferr := s.Finish(ctx, row.ID, jobs.StateFailed, reason); ferr != nil {
			return runner.Run{}, false, fmt.Errorf("failing run %s, whose definition doesn't decode: %w", row.ID, ferr)
		}
		return runner.Run{}, false, fmt.Errorf("decoding run %s's definition: %w", row.ID, err)
	}
	return runner.Run{ID: row.ID, OrgID: row.OrgID, RepoID: row.RepoID, JobID: row.JobID, Number: row.Number, Job: row.Job,
		OrgSlug: row.OrgSlug, RepoSlug: row.RepoSlug, SHA: row.Sha, Ref: row.Ref, Event: row.Trigger, Definition: job}, true, nil
}

// Token records the run's token by its hash and hint, until expiresAt, replacing one made before (runner.Store).
func (s *RunStore) Token(ctx context.Context, runID uuid.UUID, hash []byte, hint string, expiresAt time.Time) error {
	err := s.q.InsertRunToken(ctx, queries.InsertRunTokenParams{RunID: runID, TokenHash: hash, Hint: hint, ExpiresAt: expiresAt})
	return wrapIf(err, "recording run "+runID.String()+"'s token")
}

// Defer puts a queued run off until until, its job's annotation saying why (runner.Store).
func (s *RunStore) Defer(ctx context.Context, runID uuid.UUID, until time.Time, reason string) error {
	return transact(ctx, s.pool, s.q, "putting the run off", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		row, err := q.DeferRun(ctx, until, reason, runID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("putting run %s off: %w", runID, err)
		}
		in := code.AnnotationInput{Status: code.AnnotationPending, Summary: capitalize(reason)}
		return annotateRun(ctx, q, ranRun(row), in, false)
	})
}

// MarkRunning marks a queued run running and its job's annotation In progress (runner.Store).
func (s *RunStore) MarkRunning(ctx context.Context, runID uuid.UUID) (bool, error) {
	marked := false
	err := transact(ctx, s.pool, s.q, "marking the run running", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		row, err := q.MarkRunRunning(ctx, runID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("marking run %s running: %w", runID, err)
		}
		marked = true
		return annotateRun(ctx, q, ranRun(row), annotationOfState(jobs.StateRunning, ""), true)
	})
	return marked, err
}

// Finish ends a run that hasn't ended, in state with reason, sets its job's annotation to match, works out the status
// of the branches at its commit again, and deletes its token (runner.Store).
func (s *RunStore) Finish(ctx context.Context, runID uuid.UUID, state jobs.State, reason string) (bool, error) {
	finished := false
	err := transact(ctx, s.pool, s.q, "ending the run", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		if err := q.DeleteRunToken(ctx, runID); err != nil {
			return fmt.Errorf("deleting run %s's token: %w", runID, err)
		}
		row, err := q.FinishRun(ctx, string(state), reason, runID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("ending run %s: %w", runID, err)
		}
		finished = true
		return annotateRun(ctx, q, ranRun(row), annotationOfState(state, reason), true)
	})
	return finished, err
}

// annotateRun sets the run's job's annotation on its commit to in, as the job, and when moved says the run started or
// ended, works out the status of the branches there again and queues the runs the annotation triggers, the run their
// cause. Another principal holding the annotation changes nothing: the run failed as it was queued then.
func annotateRun(ctx context.Context, q *queries.Queries, r ranRun, in code.AnnotationInput, moved bool) error {
	taken, err := setJobAnnotation(ctx, q, r.OrgID, r.JobID, r.RepoID, r.Sha, r.Job, in)
	if err != nil || !moved {
		return err
	}
	if err := restatusAt(ctx, q, r.RepoID, r.Sha); err != nil || taken != "" {
		return err
	}
	e := jobs.Event{Kind: jobs.EventAnnotation, RepoID: r.RepoID, SHA: r.Sha, Name: r.Job, State: string(in.Status), ActorID: r.JobID,
		CauseRunID: r.ID}
	return queueBranchRuns(ctx, q, e)
}

// Running lists the runs of pods and scripts that run, each with the timeout its job declared (runner.Store).
func (s *RunStore) Running(ctx context.Context) ([]runner.Running, error) {
	rows, err := s.q.RunningRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing the running runs: %w", err)
	}
	out := make([]runner.Running, 0, len(rows))
	for _, r := range rows {
		var job jobs.Job
		if err := json.Unmarshal(r.Definition, &job); err != nil {
			return nil, fmt.Errorf("decoding run %s's definition: %w", r.ID, err)
		}
		out = append(out, runner.Running{ID: r.ID, OrgID: r.OrgID, RepoID: r.RepoID, Number: r.Number, StartedAt: orZero(r.StartedAt),
			Timeout: time.Duration(job.Timeout)})
	}
	return out, nil
}

// Runs finds those of ids that are runs: where each stands, and its repository (runner.Store).
func (s *RunStore) Runs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]runner.Standing, error) {
	rows, err := s.q.RunsStanding(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("finding the runs: %w", err)
	}
	out := make(map[uuid.UUID]runner.Standing, len(rows))
	for _, r := range rows {
		out[r.ID] = runner.Standing{RepoID: r.RepoID, State: jobs.State(r.State)}
	}
	return out, nil
}

// Organizations answers which of ids are organizations still (runner.Store).
func (s *RunStore) Organizations(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	out, err := s.q.OrganizationsAmong(ctx, ids)
	return out, wrapIf(err, "finding the organizations")
}

// DropTokens deletes the tokens of runs that ended without the controller, and those that expired (runner.Store).
func (s *RunStore) DropTokens(ctx context.Context) (int64, error) {
	n, err := s.q.DeleteDeadRunTokens(ctx)
	return n, wrapIf(err, "deleting the tokens of ended runs")
}

// Scheduled lists the repositories whose default branch's /cobranch.yaml declares jobs with schedules, with those jobs
// (runner.Store).
func (s *RunStore) Scheduled(ctx context.Context) ([]runner.Scheduled, error) {
	rows, err := s.q.ScheduledSettings(ctx, git.DefaultBranch)
	if err != nil {
		return nil, fmt.Errorf("listing the scheduled jobs: %w", err)
	}
	out := make([]runner.Scheduled, 0, len(rows))
	for _, r := range rows {
		var settings code.Settings
		if err := json.Unmarshal(r.Settings, &settings); err != nil {
			return nil, fmt.Errorf("decoding repository %s's settings: %w", r.RepoID, err)
		}
		scheduled := runner.Scheduled{RepoID: r.RepoID}
		for _, j := range settings.Jobs {
			if len(j.On.Schedule) > 0 {
				scheduled.Jobs = append(scheduled.Jobs, j)
			}
		}
		out = append(out, scheduled)
	}
	return out, nil
}

// QueueSlot records a slot of the schedule of the repository's job and queues the run it fires at the default
// branch's head, in one transaction that names nobody, unless a controller recorded the slot already (runner.Store).
func (s *RunStore) QueueSlot(ctx context.Context, repoID uuid.UUID, job string, slot time.Time) (bool, error) {
	queued := false
	err := transact(ctx, s.pool, s.q, "queueing a schedule's run", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		main, err := q.BranchSettings(ctx, repoID, git.DefaultBranch)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading the default branch: %w", err)
		}
		repo, err := q.RepositoryByID(ctx, repoID)
		if err != nil {
			return fmt.Errorf("reading the repository: %w", err)
		}
		_, err = q.InsertScheduleSlot(ctx, queries.InsertScheduleSlotParams{RepoID: repoID, Job: job, Slot: slot, OrgID: repo.OrgID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("recording the slot: %w", err)
		}
		queued = true
		_, err = queueRuns(ctx, q, jobs.Event{Kind: jobs.EventSchedule, RepoID: repoID, SHA: main.Head, Ref: "refs/heads/" + git.DefaultBranch,
			Name: job, Slot: slot.UTC()})
		return err
	})
	return queued, err
}

// DropSlots deletes the schedules' slots before a time (runner.Store).
func (s *RunStore) DropSlots(ctx context.Context, before time.Time) (int64, error) {
	n, err := s.q.DeleteScheduleSlots(ctx, before)
	return n, wrapIf(err, "deleting the schedules' old slots")
}
