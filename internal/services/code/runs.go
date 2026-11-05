package code

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// PermRunJobs starts, cancels and re-runs a repository's runs.
const PermRunJobs = "jobs.run"

// MaxRunsPage is the most runs a page lists.
const MaxRunsPage = 100

// Run is one run of a repository's job: at a commit and ref, for the event that queued it, of the job's definition as
// it was then (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "A run's life").
type Run struct {
	ID uuid.UUID
	// RepoID is its repository's, and OrgID that repository's organization's, in whose job namespace its pod runs.
	RepoID, OrgID uuid.UUID
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
	// Attempt counts the runs of its definition at its commit, from 1 for the run a trigger queued: a re-run is the
	// next attempt.
	Attempt int32
	// RerunOf is the number of the run its attempts started from; 0 for a first attempt.
	RerunOf int64
	// Definition is the job as it was declared when the run was queued.
	Definition jobs.Job
	State      jobs.State
	// Reason says why it waits, failed or was cancelled.
	Reason string
	// StartedBy is who started it from Waiting, or re-ran it; nil for any other run.
	StartedBy *Principal
	// CausedBy is who caused it, with their kind: StartedBy, or else its trigger's actor; nil for a schedule's run, or
	// once they are gone.
	CausedBy  *Actor
	QueuedAt  time.Time
	StartedAt *time.Time
	EndedAt   *time.Time
	// Version counts its changes from 1: its ETag.
	Version int64
}

// RunsQuery picks a page of a repository's runs, newest first: those before the run numbered Before (0 for the
// newest), at most Limit of them (0 for DefaultPageSize), kept by each filter that isn't empty: at commit SHA, of the
// job named Job, on branch Branch, in one of States, and those whose job's name or branch or tag holds Query, ignoring
// case, or whose commit starts with it.
type RunsQuery struct {
	SHA    string
	Job    string
	Branch string
	States []jobs.State
	Query  string
	Before int64
	Limit  int
}

// RunPage is a page of a repository's runs, newest first, and the number the next page lists runs before; 0 after the
// last. The first page counts every run its query keeps in Total; a later page counts none.
type RunPage struct {
	Runs  []Run
	Next  int64
	Total *int
}

// Errors of runs.
var (
	// ErrRunEnded is a run asked to change that has already ended.
	ErrRunEnded = errors.New("the run has ended")
	// ErrRunNotWaiting is a run asked to start that doesn't wait for a member.
	ErrRunNotWaiting = errors.New("the run doesn't wait for a member to start it")
	// ErrRunActive is a run asked for while its job's run at the commit, or the run asked to run again, hasn't ended.
	ErrRunActive = errors.New("a run of this job at this commit hasn't ended")
	// ErrNotManual is a job asked to run that declares no manual trigger.
	ErrNotManual = errors.New("the job takes no manual start")
)

// EventInput is an event a principal posts to a repository: its name, the branch its runs run on (the default branch
// when empty), and the data they read, any JSON up to jobs.MaxEventData bytes.
type EventInput struct {
	Name   string
	Branch string
	Data   json.RawMessage
}

// RunStore keeps a repository's runs.
type RunStore interface {
	// Runs lists at most q.Limit of the repository's runs that q keeps, newest first, numbered before q.Before (0 for the
	// newest).
	Runs(ctx context.Context, repoID uuid.UUID, q RunsQuery) ([]Run, error)
	// CountRuns counts the repository's runs that q's filters keep, whatever its page.
	CountRuns(ctx context.Context, repoID uuid.UUID, q RunsQuery) (int, error)
	// Run finds one of the repository's runs by number, or repos.ErrNotFound.
	Run(ctx context.Context, repoID uuid.UUID, number int64) (Run, error)
	// CancelRun cancels a run that hasn't ended, for actor, in one transaction that sets its job's annotation Failed,
	// appends event run.cancelled and works out again the status of the branches at its commit: repos.ErrNotFound
	// without it, ErrRunEnded once it ended.
	CancelRun(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, number int64) (Run, error)
	// StartRun queues a waiting run for actor, in one transaction that appends event run.started: repos.ErrNotFound
	// without it, ErrRunNotWaiting when it doesn't wait.
	StartRun(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, number int64) (Run, error)
	// RerunRun queues an ended run's definition again at its commit and ref, as its next attempt, for actor, in one
	// transaction that appends event run.rerun: repos.ErrNotFound without it, ErrRunActive while it, or another run of
	// its job at its commit, hasn't ended.
	RerunRun(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, number int64) (Run, error)
	// RunJob queues a run of the job its branch's head declares by name, at that head, for actor, in one transaction
	// that appends event job.run: repos.ErrNotFound without the branch or the job, ErrNotManual when the job declares
	// no manual trigger, ErrRunActive when one of its runs at the head hasn't ended.
	RunJob(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, branch, job string) (Run, error)
	// PostEvent queues the runs of the default branch's jobs that follow the event, at the head of the branch it names,
	// for actor, in one transaction that appends event event.posted, and answers them: a ValidationError naming the
	// branch when it isn't live.
	PostEvent(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, in EventInput) ([]Run, error)
}

// Runs lists a page of the repository's runs that q keeps, newest first, and on the first page counts them all; it takes
// repo.read.
func (s *Service) Runs(ctx context.Context, v Viewer, repoID uuid.UUID, q RunsQuery) (RunPage, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Runs")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.commit.sha", q.SHA),
		attribute.String("cobranch.job", q.Job), attribute.String("cobranch.branch", q.Branch))
	if err := s.read(ctx, v, repoID); err != nil {
		return RunPage{}, failed(span, "reading the runs", err)
	}
	if err := validateRunsQuery(q); err != nil {
		return RunPage{}, err
	}
	if q.Limit == 0 {
		q.Limit = DefaultPageSize
	}
	limit := q.Limit
	q.Limit++
	list, err := s.store.Runs(ctx, repoID, q)
	if err != nil {
		return RunPage{}, failed(span, "reading the runs", err)
	}
	page := RunPage{Runs: list}
	if len(list) > limit {
		page.Runs = list[:limit]
		page.Next = list[limit-1].Number
	}
	if q.Before == 0 {
		n, err := s.store.CountRuns(ctx, repoID, q)
		if err != nil {
			return RunPage{}, failed(span, "counting the runs", err)
		}
		page.Total = &n
	}
	return page, nil
}

// validateRunsQuery answers what is wrong with q: a commit that isn't one's full name, a job's or a branch's name past
// 255 bytes or holding a control character, a state no run is in, a query MaxQuery doesn't allow, or a page out of
// bounds.
func validateRunsQuery(q RunsQuery) error {
	if q.SHA != "" {
		if err := ValidateSHA("sha", q.SHA); err != nil {
			return err
		}
	}
	for field, name := range map[string]string{"job": q.Job, "branch": q.Branch} {
		if len(name) > 255 || strings.ContainsFunc(name, unicode.IsControl) || !utf8.ValidString(name) {
			return &orgs.ValidationError{Field: field, Code: field + ".invalid", Message: "a name of up to 255 bytes, on one line"}
		}
	}
	for _, st := range q.States {
		if !slices.Contains(runStates, st) {
			return &orgs.ValidationError{Field: "state", Code: "state.invalid", Message: fmt.Sprintf("one of %v", runStates)}
		}
	}
	if err := validateQuery(q.Query); err != nil {
		return err
	}
	if q.Limit < 0 || q.Limit > MaxRunsPage || q.Before < 0 {
		return &orgs.ValidationError{Field: "limit", Code: "limit.invalid", Message: fmt.Sprintf("at most %d runs a page", MaxRunsPage)}
	}
	return nil
}

// runStates are the states a run may be in, which a RunsQuery may keep.
var runStates = []jobs.State{jobs.StateWaiting, jobs.StateQueued, jobs.StateRunning, jobs.StateSucceeded, jobs.StateFailed, jobs.StateCancelled}

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

// RerunRun runs one of the repository's ended runs again, as the next attempt of its definition at its commit, for v;
// it takes jobs.run. A run that hasn't ended, or another run of its job at its commit that hasn't, is ErrRunActive.
func (s *Service) RerunRun(ctx context.Context, v Viewer, repoID uuid.UUID, number int64) (Run, error) {
	ctx, span := tracer.Start(ctx, "code.Service.RerunRun")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.Int64("cobranch.run.number", number))
	if err := s.runJobs(ctx, v, repoID); err != nil {
		return Run{}, failed(span, "re-running the run", err)
	}
	r, err := s.store.RerunRun(ctx, v.ID, repoID, number)
	if err != nil {
		return Run{}, failed(span, "re-running the run", err)
	}
	return r, nil
}

// RunJob starts the job of name that a branch's head declares with a manual trigger, at that head, for v; it takes
// jobs.run. A job without a manual trigger is ErrNotManual, and one with a run at the head that hasn't ended
// ErrRunActive.
func (s *Service) RunJob(ctx context.Context, v Viewer, repoID uuid.UUID, branch, job string) (Run, error) {
	ctx, span := tracer.Start(ctx, "code.Service.RunJob")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", branch),
		attribute.String("cobranch.job", job))
	if err := s.runJobs(ctx, v, repoID); err != nil {
		return Run{}, failed(span, "running the job", err)
	}
	if ValidateAnnotationName(job) != nil {
		return Run{}, failed(span, "running the job", repos.ErrNotFound)
	}
	r, err := s.store.RunJob(ctx, v.ID, repoID, branch, job)
	if err != nil {
		return Run{}, failed(span, "running the job", err)
	}
	return r, nil
}

// PostEvent posts an event to a repository for v, which queues the runs of the default branch's jobs that follow its
// name at the head of the branch it names, the default branch when it names none, and answers them; it takes
// jobs.run. Its name is written as an annotation's, and its data is JSON of at most jobs.MaxEventData bytes.
func (s *Service) PostEvent(ctx context.Context, v Viewer, repoID uuid.UUID, in EventInput) ([]Run, error) {
	ctx, span := tracer.Start(ctx, "code.Service.PostEvent")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.event", in.Name))
	if err := s.runJobs(ctx, v, repoID); err != nil {
		return nil, failed(span, "posting the event", err)
	}
	if in.Branch == "" {
		in.Branch = git.DefaultBranch
	}
	if err := validateEvent(in); err != nil {
		return nil, err
	}
	runs, err := s.store.PostEvent(ctx, v.ID, repoID, in)
	if err != nil {
		return nil, failed(span, "posting the event", err)
	}
	return runs, nil
}

// validateEvent reports whether in is an event a principal may post: a name written as an annotation's, and data that
// is JSON of at most jobs.MaxEventData bytes.
func validateEvent(in EventInput) error {
	if err := ValidateAnnotationName(in.Name); err != nil {
		return &orgs.ValidationError{Field: "name", Code: "name.invalid",
			Message: "an event's name: 1 to 100 lowercase letters, digits, '-', '_', '.' and '/', starting with a letter or digit"}
	}
	if len(in.Data) > jobs.MaxEventData || (len(in.Data) > 0 && !json.Valid(in.Data)) {
		return &orgs.ValidationError{Field: "data", Code: "data.invalid", Message: fmt.Sprintf("an event's data is JSON of at most %d KiB", jobs.MaxEventData>>10)}
	}
	return nil
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
