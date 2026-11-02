package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/jobs"
)

// recordSettings stores, in the push's transaction, the /cobranch.yaml at each branch tip the push moves, and names
// it on its branch, which the jobs of that tip then read
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "/cobranch.yaml").
func recordSettings(ctx context.Context, q *queries.Queries, orgID uuid.UUID, push git.Push) error {
	var files []queries.InsertSettingsFileParams
	branches := make([]queries.SetBranchSettingsParams, 0, len(push.Settings))
	for _, tip := range push.Settings {
		var blob *string
		if tip.Blob != "" {
			blob = &tip.Blob
			files = append(files, queries.InsertSettingsFileParams{RepoID: push.RepoID, Blob: tip.Blob, OrgID: orgID,
				Settings: tip.Settings, Error: tip.Error})
		}
		branches = append(branches, queries.SetBranchSettingsParams{Blob: blob, RepoID: push.RepoID, Name: tip.Branch})
	}
	var failed error
	q.InsertSettingsFile(ctx, files).Exec(func(i int, err error) {
		if err != nil && failed == nil {
			failed = fmt.Errorf("recording /%s %s: %w", git.SettingsFile, files[i].Blob, err)
		}
	})
	if failed != nil {
		return failed
	}
	q.SetBranchSettings(ctx, branches).Exec(func(i int, err error) {
		if err != nil && failed == nil {
			failed = fmt.Errorf("naming the settings of branch %s: %w", branches[i].Name, err)
		}
	})
	return failed
}

// queuePushRuns queues the runs the claimed push triggers, in the push worker's transaction: for each branch it
// creates or moves, and each tag that names a commit, the jobs that match.
func queuePushRuns(ctx context.Context, q *queries.Queries, claimed queries.ClaimPushRow) error {
	var updates []refUpdateJSON
	if err := json.Unmarshal(claimed.RefUpdates, &updates); err != nil {
		return fmt.Errorf("reading the push's ref updates: %w", err)
	}
	var peeled map[string]string
	if err := json.Unmarshal(claimed.Peeled, &peeled); err != nil {
		return fmt.Errorf("reading the push's tags: %w", err)
	}
	pushed := jobs.Event{RepoID: claimed.RepoID, PushID: claimed.ID}
	if claimed.PusherID != nil {
		pushed.ActorID = *claimed.PusherID
	}
	for _, u := range updates {
		e, ok, err := pushEvent(ctx, q, pushed, u, peeled[u.Name])
		switch {
		case err != nil:
			return err
		case !ok:
			continue
		}
		if err := queueRuns(ctx, q, e); err != nil {
			return err
		}
	}
	return nil
}

// pushEvent is the event a push's update of a ref makes, peeled being the commit a tag names: a push to a branch it
// creates or moves, or a tag that names a commit and still names what the push set it to; none for a deleted ref, or
// a tag that moved on since. The later push that moved it, which another push worker may have processed first, makes
// that tag's event.
func pushEvent(ctx context.Context, q *queries.Queries, pushed jobs.Event, u refUpdateJSON, peeled string) (jobs.Event, bool, error) {
	e := pushed
	e.Ref = u.Name
	_, isBranch := git.Branch(u.Name)
	switch {
	case u.New == git.ZeroSHA:
		return e, false, nil
	case isBranch:
		e.Kind, e.SHA = jobs.EventPush, u.New
		return e, true, nil
	case peeled == "":
		return e, false, nil
	}
	current, err := q.RefSHA(ctx, e.RepoID, u.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, false, nil
	}
	if err != nil {
		return e, false, fmt.Errorf("reading tag %s: %w", u.Name, err)
	}
	e.Kind, e.SHA = jobs.EventTag, peeled
	return e, current == u.New, nil
}

// queueRuns queues, in the transaction of the change that caused e, a run of each job whose triggers match it, by
// name, each with its job's annotation set on e's commit: Pending, or Failed with the reason when it can't run. Jobs
// come from e's branch's head for events on a branch, which must still be e's commit, and from the default branch's
// head for tags, posted events and schedules. The branches at e's commit get their status worked out again, unless they
// wait for the push worker, which then does.
func queueRuns(ctx context.Context, q *queries.Queries, e jobs.Event) error {
	orgID, err := q.CommitOrg(ctx, e.RepoID, e.SHA)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("finding commit %s: %w", e.SHA, err)
	}
	defined, err := definitionsFor(ctx, q, e)
	if err != nil || len(defined) == 0 {
		return err
	}
	if err := q.LockRunQueue(ctx, e.RepoID); err != nil {
		return fmt.Errorf("locking the repository's runs: %w", err)
	}
	wait := false
	if e.Kind == jobs.EventPush {
		if wait, err = waitsForMember(ctx, q, e.RepoID, e.ActorID); err != nil {
			return err
		}
	}
	main, err := mainSettings(ctx, q, e.RepoID)
	if err != nil {
		return err
	}
	queued := false
	for _, job := range defined {
		if !job.Matches(e) {
			continue
		}
		if err := queueRun(ctx, q, newRun{orgID: orgID, event: e, job: job, wait: wait, ceiling: main.Repository.Ceiling}); err != nil {
			return err
		}
		queued = true
	}
	if !queued {
		return nil
	}
	return restatusAt(ctx, q, e.RepoID, e.SHA)
}

// definitionsFor reads the jobs that may answer e: none when its branch moved on from its commit, or when the file
// there doesn't parse or doesn't exist.
func definitionsFor(ctx context.Context, q *queries.Queries, e jobs.Event) ([]jobs.Job, error) {
	branch, onBranch := git.Branch(e.Ref)
	switch e.Kind {
	case jobs.EventTag, jobs.EventPosted, jobs.EventSchedule:
		branch, onBranch = git.DefaultBranch, false
	}
	head, settings, err := settingsOf(ctx, q, e.RepoID, branch)
	if err != nil || settings == nil || (onBranch && head != e.SHA) {
		return nil, err
	}
	return settings.Jobs, nil
}

// mainSettings reads what the default branch's /cobranch.yaml says; the defaults without one, or without the branch.
func mainSettings(ctx context.Context, q *queries.Queries, repoID uuid.UUID) (code.Settings, error) {
	_, settings, err := settingsOf(ctx, q, repoID, git.DefaultBranch)
	if err != nil {
		return code.Settings{}, err
	}
	if settings == nil {
		return code.Settings{Repository: jobs.DefaultRepositorySettings}, nil
	}
	return *settings, nil
}

// settingsOf reads a live branch's head and what the /cobranch.yaml there says: nil without the branch or the file,
// or when the file doesn't parse.
func settingsOf(ctx context.Context, q *queries.Queries, repoID uuid.UUID, branch string) (string, *code.Settings, error) {
	row, err := q.BranchSettings(ctx, repoID, branch)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("reading the settings of branch %s: %w", branch, err)
	}
	if row.Settings == nil {
		return row.Head, nil, nil
	}
	var settings code.Settings
	if err := json.Unmarshal(row.Settings, &settings); err != nil {
		return "", nil, fmt.Errorf("decoding the settings of branch %s: %w", branch, err)
	}
	return row.Head, &settings, nil
}

// waitsForMember reports whether work that follows a push by pusherID into the repository waits for a member to
// start it: when the default branch's /cobranch.yaml leaves repository.outsidePushes at wait, and the pusher is
// Anonymous, a user who isn't a member of the repository's organization, or gone. Members, API keys, agents and jobs
// push from inside (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "The
// outside-push gate").
func waitsForMember(ctx context.Context, q *queries.Queries, repoID, pusherID uuid.UUID) (bool, error) {
	main, err := mainSettings(ctx, q, repoID)
	if err != nil || main.Repository.OutsidePushes == jobs.OutsideRun {
		return false, err
	}
	repo, err := q.RepositoryByID(ctx, repoID)
	if err != nil {
		return false, fmt.Errorf("reading the repository: %w", err)
	}
	standing, err := q.PrincipalStanding(ctx, repo.OrgID, pusherID)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading who pushed: %w", err)
	}
	return standing.Kind == "anonymous" || (standing.Kind == "user" && !standing.Member), nil
}

// newRun is a run queueRun queues: of job, for event, at its commit in the organization; waiting for a member when
// wait is true; within the permissions of the repository role ceiling names.
type newRun struct {
	orgID   uuid.UUID
	event   jobs.Event
	job     jobs.Job
	wait    bool
	ceiling string
}

// queueRun queues one run of a job, unless one of its runs that hasn't ended is at the same commit already, whose
// file, and so whose job, is the same: one waiting for a member gives way to one that doesn't. It supersedes the job's
// runs that haven't ended on the same ref, sets the job's annotation, and records the run Waiting, Queued, or Failed
// when the job lists more than its ceiling or another principal holds its annotation there.
func queueRun(ctx context.Context, q *queries.Queries, r newRun) error {
	jobID, err := jobPrincipal(ctx, q, r.orgID, r.event.RepoID, r.job.Name)
	if err != nil {
		return err
	}
	active, err := q.ActiveRunAt(ctx, jobID, r.event.SHA)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return fmt.Errorf("finding job %s's runs at %s: %w", r.job.Name, r.event.SHA, err)
	case jobs.State(active.State) != jobs.StateWaiting || r.wait:
		return nil
	}
	number, err := q.NextRunNumber(ctx, r.event.RepoID)
	if err != nil {
		return fmt.Errorf("numbering the run: %w", err)
	}
	if active.ID != uuid.Nil {
		if err := q.EndRun(ctx, string(jobs.StateCancelled), fmt.Sprintf("superseded by #%d", number), active.ID); err != nil {
			return fmt.Errorf("superseding a waiting run of job %s: %w", r.job.Name, err)
		}
	}
	if err := supersede(ctx, q, r, jobID, number); err != nil {
		return err
	}
	state, reason, err := initialState(ctx, q, r)
	if err != nil {
		return err
	}
	taken, err := setJobAnnotation(ctx, q, r.orgID, jobID, r.event.RepoID, r.event.SHA, r.job.Name, annotationOfState(state, reason))
	switch {
	case err != nil:
		return err
	case taken != "":
		state, reason = jobs.StateFailed, "another principal, "+taken+", holds annotation "+r.job.Name+" on this commit"
	}
	return insertRun(ctx, q, r, jobID, number, state, reason)
}

// initialState is the state a run starts in: Failed when its job lists permissions beyond its ceiling, Waiting when
// it waits for a member, and Queued otherwise; with the reason.
func initialState(ctx context.Context, q *queries.Queries, r newRun) (jobs.State, string, error) {
	allowed, err := q.RepositoryRolePermissions(ctx, r.ceiling)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.StateFailed, "repository.jobs.ceiling names no repository role: " + r.ceiling, nil
	}
	if err != nil {
		return "", "", fmt.Errorf("reading the jobs' ceiling: %w", err)
	}
	for _, p := range r.job.Permissions {
		if !slices.Contains(allowed, p) {
			return jobs.StateFailed, fmt.Sprintf("the job lists %s, beyond its ceiling, %s", p, r.ceiling), nil
		}
	}
	if r.wait {
		return jobs.StateWaiting, reasonWaiting, nil
	}
	return jobs.StateQueued, "", nil
}

// reasonWaiting is why a run of a tip pushed from outside the organization waits.
const reasonWaiting = "waits for a member to start it"

// annotationOfState is what a run's job's annotation says of a run that just reached state, for reason.
func annotationOfState(state jobs.State, reason string) code.AnnotationInput {
	switch state {
	case jobs.StateQueued:
		return code.AnnotationInput{Status: code.AnnotationPending, Summary: "Queued"}
	case jobs.StateWaiting:
		return code.AnnotationInput{Status: code.AnnotationPending, Summary: capitalize(reason)}
	case jobs.StateRunning:
		return code.AnnotationInput{Status: code.AnnotationInProgress, Summary: "Running"}
	case jobs.StateSucceeded:
		return code.AnnotationInput{Status: code.AnnotationSuccessful, Summary: capitalize(reason)}
	}
	return code.AnnotationInput{Status: code.AnnotationFailed, Summary: capitalize(reason)}
}

// capitalize starts s with a capital letter, as an annotation's summary does.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
