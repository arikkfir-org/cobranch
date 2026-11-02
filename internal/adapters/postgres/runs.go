package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/repos"
)

// jobPrincipal finds the principal of the repository's job of name, making it on the job's first run. Two push
// workers may make the same job's at once: the second's insert, in a savepoint of the transaction it runs in, rolls
// back, and it finds the first's.
func jobPrincipal(ctx context.Context, q *queries.Queries, orgID, repoID uuid.UUID, name string) (uuid.UUID, error) {
	id, err := q.JobPrincipal(ctx, repoID, name)
	if err == nil || !errors.Is(err, pgx.ErrNoRows) {
		return id, wrapIf(err, "finding job "+name)
	}
	if id, err = uuid.NewV7(); err != nil {
		return uuid.Nil, fmt.Errorf("making an ID: %w", err)
	}
	inserted := transact(ctx, nil, q, "making job "+name+"'s principal", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		return q.InsertJob(ctx, queries.InsertJobParams{ID: id, Name: name, OrgID: orgID, RepoID: repoID})
	})
	var conflict *ConflictError
	if errors.As(inserted, &conflict) {
		id, err = q.JobPrincipal(ctx, repoID, name)
		return id, wrapIf(err, "finding job "+name)
	}
	return id, inserted
}

// wrapIf wraps err with what was being done, or answers nil without one.
func wrapIf(err error, what string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", what, err)
}

// supersede cancels the job's runs that haven't ended on the new run's ref at other commits, as superseded by the run
// numbered number, failing their annotations there, whose branches' status it works out again.
func supersede(ctx context.Context, q *queries.Queries, r newRun, jobID uuid.UUID, number int64) error {
	reason := fmt.Sprintf("superseded by #%d", number)
	rows, err := q.SupersedeRuns(ctx, queries.SupersedeRunsParams{Reason: reason, JobID: jobID, Ref: r.event.Ref, Sha: r.event.SHA})
	if err != nil {
		return fmt.Errorf("superseding job %s's runs: %w", r.job.Name, err)
	}
	for _, row := range rows {
		if _, err := setJobAnnotation(ctx, q, r.orgID, jobID, r.event.RepoID, row.Sha, r.job.Name, annotationOfState(jobs.StateCancelled, reason)); err != nil {
			return err
		}
		if err := restatusAt(ctx, q, r.event.RepoID, row.Sha); err != nil {
			return err
		}
	}
	return nil
}

// setJobAnnotation sets the job's annotation of name on the commit to in, as the job, unless another principal holds
// it there: then it changes nothing and names them.
func setJobAnnotation(ctx context.Context, q *queries.Queries, orgID, jobID, repoID uuid.UUID, sha, name string, in code.AnnotationInput) (string, error) {
	in.Name, in.Version = name, 0
	row, created, err := lockAnnotation(ctx, q, jobID, orgID, repoID, sha, in)
	switch {
	case err != nil || created:
		return "", err
	case row.Principal == nil:
		return "a principal who is gone", nil
	case row.Principal.ID != jobID:
		return row.Principal.DisplayName, nil
	case row.Status == in.Status && row.Summary == in.Summary && row.Link == in.Link:
		return "", nil
	}
	updated, err := q.UpdateAnnotation(ctx, queries.UpdateAnnotationParams{Status: string(in.Status), Summary: in.Summary, Link: in.Link, ID: row.ID})
	if err != nil {
		return "", fmt.Errorf("changing annotation %s: %w", name, err)
	}
	return "", annotationEvent(ctx, q, orgID, jobID, "set", repoID, annotationOf(updated, &row.Principal.DisplayName))
}

// insertRun records a run of the job its principal names, numbered number, in state for reason.
func insertRun(ctx context.Context, q *queries.Queries, r newRun, jobID uuid.UUID, number int64, state jobs.State, reason string) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("making an ID: %w", err)
	}
	trigger, err := json.Marshal(r.event)
	if err != nil {
		return fmt.Errorf("encoding the run's trigger: %w", err)
	}
	definition, err := json.Marshal(r.job)
	if err != nil {
		return fmt.Errorf("encoding the run's definition: %w", err)
	}
	var cause *uuid.UUID
	if r.event.CauseRunID != uuid.Nil {
		cause = &r.event.CauseRunID
	}
	if err := q.InsertRun(ctx, queries.InsertRunParams{ID: id, OrgID: r.orgID, RepoID: r.event.RepoID, Number: number, JobID: jobID,
		Sha: r.event.SHA, Ref: r.event.Ref, Trigger: trigger, CauseRunID: cause, Definition: definition, State: string(state),
		Reason: reason}); err != nil {
		return fmt.Errorf("queueing run #%d of job %s: %w", number, r.job.Name, err)
	}
	return nil
}

// Runs lists at most limit of the repository's runs, newest first, numbered before before (0 for the newest), at
// commit sha unless it is empty (code.Store).
func (s *CodeStore) Runs(ctx context.Context, repoID uuid.UUID, sha string, before int64, limit int) ([]code.Run, error) {
	rows, err := s.q.RunsOf(ctx, queries.RunsOfParams{RepoID: repoID, Before: before, Sha: sha, Lim: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("listing the runs: %w", err)
	}
	out := make([]code.Run, 0, len(rows))
	for _, r := range rows {
		run, err := runOf(ctx, s.q, r.JobRun, r.Job, r.StartedByName)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}

// Run finds one of the repository's runs by number, or repos.ErrNotFound (code.Store).
func (s *CodeStore) Run(ctx context.Context, repoID uuid.UUID, number int64) (code.Run, error) {
	row, err := s.q.RunByNumber(ctx, repoID, number)
	if errors.Is(err, pgx.ErrNoRows) {
		return code.Run{}, repos.ErrNotFound
	}
	if err != nil {
		return code.Run{}, fmt.Errorf("finding run #%d: %w", number, err)
	}
	return runOf(ctx, s.q, row.JobRun, row.Job, row.StartedByName)
}

// CancelRun cancels a run that hasn't ended, for actor, in one transaction that acts for actor: its job's annotation
// fails naming them, event run.cancelled names the run, and the branches at its commit get their status worked out
// again (code.Store).
func (s *CodeStore) CancelRun(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, number int64) (code.Run, error) {
	err := transact(ctx, s.pool, s.q, "cancelling the run", actor, func(ctx context.Context, q *queries.Queries) error {
		row, err := runForUpdate(ctx, q, repoID, number)
		if err != nil {
			return err
		}
		if !jobs.State(row.JobRun.State).Active() {
			return code.ErrRunEnded
		}
		name, err := q.PrincipalName(ctx, actor)
		if err != nil {
			return fmt.Errorf("naming who cancels: %w", err)
		}
		reason := "cancelled by " + name
		if err := q.EndRun(ctx, string(jobs.StateCancelled), reason, row.JobRun.ID); err != nil {
			return fmt.Errorf("cancelling run #%d: %w", number, err)
		}
		r := row.JobRun
		if _, err := setJobAnnotation(ctx, q, r.OrgID, r.JobID, repoID, r.Sha, row.Job, annotationOfState(jobs.StateCancelled, reason)); err != nil {
			return err
		}
		if err := orgEvent(ctx, q, &r.OrgID, actor, "run.cancelled", r.ID, map[string]any{"repo": repoID, "number": number}); err != nil {
			return err
		}
		return restatusAt(ctx, q, repoID, r.Sha)
	})
	if err != nil {
		return code.Run{}, err
	}
	return s.Run(ctx, repoID, number)
}

// StartRun queues a waiting run for actor, in one transaction that acts for actor and appends event run.started; its
// job's annotation stays Pending, now Queued (code.Store).
func (s *CodeStore) StartRun(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, number int64) (code.Run, error) {
	err := transact(ctx, s.pool, s.q, "starting the run", actor, func(ctx context.Context, q *queries.Queries) error {
		row, err := runForUpdate(ctx, q, repoID, number)
		if err != nil {
			return err
		}
		r := row.JobRun
		if jobs.State(r.State) != jobs.StateWaiting {
			return code.ErrRunNotWaiting
		}
		if err := q.StartWaitingRun(ctx, &actor, r.ID); err != nil {
			return fmt.Errorf("starting run #%d: %w", number, err)
		}
		if _, err := setJobAnnotation(ctx, q, r.OrgID, r.JobID, repoID, r.Sha, row.Job, annotationOfState(jobs.StateQueued, "")); err != nil {
			return err
		}
		return orgEvent(ctx, q, &r.OrgID, actor, "run.started", r.ID, map[string]any{"repo": repoID, "number": number})
	})
	if err != nil {
		return code.Run{}, err
	}
	return s.Run(ctx, repoID, number)
}

// runForUpdate finds a run by number and locks it, or answers repos.ErrNotFound.
func runForUpdate(ctx context.Context, q *queries.Queries, repoID uuid.UUID, number int64) (queries.RunForUpdateRow, error) {
	row, err := q.RunForUpdate(ctx, repoID, number)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, repos.ErrNotFound
	}
	if err != nil {
		return row, fmt.Errorf("finding run #%d: %w", number, err)
	}
	return row, nil
}

// runOf is the domain's view of a job_runs row, with its job's name and who started it, if anyone; the number of the
// run that caused it is read when there is one.
func runOf(ctx context.Context, q *queries.Queries, r queries.JobRun, job string, startedBy *string) (code.Run, error) {
	out := code.Run{ID: r.ID, Number: r.Number, Job: code.Principal{ID: r.JobID, DisplayName: job}, SHA: r.Sha, Ref: r.Ref,
		State: jobs.State(r.State), Reason: r.Reason, StartedBy: principalOf(r.StartedBy, startedBy), QueuedAt: r.QueuedAt,
		StartedAt: r.StartedAt, EndedAt: r.EndedAt, Version: r.Version}
	if err := json.Unmarshal(r.Trigger, &out.Trigger); err != nil {
		return code.Run{}, fmt.Errorf("decoding run #%d's trigger: %w", r.Number, err)
	}
	out.Trigger.RepoID = r.RepoID
	if err := json.Unmarshal(r.Definition, &out.Definition); err != nil {
		return code.Run{}, fmt.Errorf("decoding run #%d's definition: %w", r.Number, err)
	}
	if r.CauseRunID != nil {
		cause, err := q.RunNumber(ctx, *r.CauseRunID)
		if err != nil {
			return code.Run{}, fmt.Errorf("finding run #%d's cause: %w", r.Number, err)
		}
		out.CauseRun = cause
	}
	return out, nil
}
