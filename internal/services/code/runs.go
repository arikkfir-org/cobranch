package code

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/orgs"
)

// PermRunJobs starts, cancels and re-runs a repository's runs.
const PermRunJobs = "jobs.run"

// MaxRunsPage is the most runs a page lists.
const MaxRunsPage = 100

// Run is one run of a repository's job: at a commit and ref, for the event that queued it, of the job's definition as
// it was then (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "A run's life").
type Run struct {
	ID uuid.UUID
	// Number counts the repository's runs from 1.
	Number int64
	// Job is the job's principal, named as the job.
	Job Principal
	SHA string
	// Ref is the branch or tag it runs on, in full.
	Ref string
	// Trigger is the event that queued it.
	Trigger jobs.Event
	// CauseRun is the number of the run whose annotation triggered it; 0 for any other.
	CauseRun int64
	// Definition is the job as it was declared when the run was queued.
	Definition jobs.Job
	State      jobs.State
	// Reason says why it waits, failed or was cancelled.
	Reason string
	// StartedBy is who started it from Waiting; nil for a run that never waited.
	StartedBy *Principal
	QueuedAt  time.Time
	StartedAt *time.Time
	EndedAt   *time.Time
	// Version counts its changes from 1: its ETag.
	Version int64
}

// RunPage is a page of a repository's runs, newest first, and the number the next page lists runs before; 0 after the
// last.
type RunPage struct {
	Runs []Run
	Next int64
}

// Errors of runs.
var (
	// ErrRunEnded is a run asked to change that has already ended.
	ErrRunEnded = errors.New("the run has ended")
	// ErrRunNotWaiting is a run asked to start that doesn't wait for a member.
	ErrRunNotWaiting = errors.New("the run doesn't wait for a member to start it")
)

// RunStore keeps a repository's runs.
type RunStore interface {
	// Runs lists at most limit of the repository's runs, newest first, numbered before before (0 for the newest), at
	// commit sha unless it is empty.
	Runs(ctx context.Context, repoID uuid.UUID, sha string, before int64, limit int) ([]Run, error)
	// Run finds one of the repository's runs by number, or repos.ErrNotFound.
	Run(ctx context.Context, repoID uuid.UUID, number int64) (Run, error)
	// CancelRun cancels a run that hasn't ended, for actor, in one transaction that sets its job's annotation Failed,
	// appends event run.cancelled and works out again the status of the branches at its commit: repos.ErrNotFound
	// without it, ErrRunEnded once it ended.
	CancelRun(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, number int64) (Run, error)
	// StartRun queues a waiting run for actor, in one transaction that appends event run.started: repos.ErrNotFound
	// without it, ErrRunNotWaiting when it doesn't wait.
	StartRun(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, number int64) (Run, error)
}

// Runs lists a page of the repository's runs, newest first, at commit sha unless it is empty, before the run numbered
// before (0 for the newest); it takes repo.read. limit 0 asks for DefaultPageSize.
func (s *Service) Runs(ctx context.Context, v Viewer, repoID uuid.UUID, sha string, before int64, limit int) (RunPage, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Runs")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.commit.sha", sha))
	if err := s.read(ctx, v, repoID); err != nil {
		return RunPage{}, failed(span, "reading the runs", err)
	}
	if sha != "" {
		if err := ValidateSHA("sha", sha); err != nil {
			return RunPage{}, err
		}
	}
	if limit < 0 || limit > MaxRunsPage || before < 0 {
		return RunPage{}, &orgs.ValidationError{Field: "limit", Code: "limit.invalid", Message: fmt.Sprintf("at most %d runs a page", MaxRunsPage)}
	}
	if limit == 0 {
		limit = DefaultPageSize
	}
	list, err := s.store.Runs(ctx, repoID, sha, before, limit+1)
	if err != nil {
		return RunPage{}, failed(span, "reading the runs", err)
	}
	page := RunPage{Runs: list}
	if len(list) > limit {
		page.Runs = list[:limit]
		page.Next = list[limit-1].Number
	}
	return page, nil
}

// Run finds one of the repository's runs by its number; it takes repo.read. A run that isn't there is
// repos.ErrNotFound.
func (s *Service) Run(ctx context.Context, v Viewer, repoID uuid.UUID, number int64) (Run, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Run")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.Int64("cobranch.run.number", number))
	if err := s.read(ctx, v, repoID); err != nil {
		return Run{}, failed(span, "reading the run", err)
	}
	r, err := s.store.Run(ctx, repoID, number)
	if err != nil {
		return Run{}, failed(span, "reading the run", err)
	}
	return r, nil
}

// CancelRun cancels one of the repository's runs that hasn't ended, for v, whose job's annotation then fails naming
// them; it takes jobs.run. A run that has ended is ErrRunEnded.
func (s *Service) CancelRun(ctx context.Context, v Viewer, repoID uuid.UUID, number int64) (Run, error) {
	ctx, span := tracer.Start(ctx, "code.Service.CancelRun")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.Int64("cobranch.run.number", number))
	if err := s.runJobs(ctx, v, repoID); err != nil {
		return Run{}, failed(span, "cancelling the run", err)
	}
	r, err := s.store.CancelRun(ctx, v.ID, repoID, number)
	if err != nil {
		return Run{}, failed(span, "cancelling the run", err)
	}
	return r, nil
}

// StartRun queues one of the repository's runs that waits for a member, its tip pushed from outside the organization,
// for v; it takes jobs.run. A run that doesn't wait is ErrRunNotWaiting.
func (s *Service) StartRun(ctx context.Context, v Viewer, repoID uuid.UUID, number int64) (Run, error) {
	ctx, span := tracer.Start(ctx, "code.Service.StartRun")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.Int64("cobranch.run.number", number))
	if err := s.runJobs(ctx, v, repoID); err != nil {
		return Run{}, failed(span, "starting the run", err)
	}
	r, err := s.store.StartRun(ctx, v.ID, repoID, number)
	if err != nil {
		return Run{}, failed(span, "starting the run", err)
	}
	return r, nil
}

// runJobs checks that v holds jobs.run on the repository: repos.ErrNotFound without repo.read.
func (s *Service) runJobs(ctx context.Context, v Viewer, repoID uuid.UUID) error {
	held, err := s.holds(ctx, v, repoID)
	if err != nil {
		return err
	}
	if !slices.Contains(held, PermRunJobs) {
		return fmt.Errorf("%w: %s on the repository", auth.ErrForbidden, PermRunJobs)
	}
	return nil
}
