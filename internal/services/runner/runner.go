// Package runner is the controller's domain: it takes the queued runs of pods and scripts, starts each in its
// organization's job namespace with the secrets its job may read and a token of its own, follows it to its end, keeps
// its logs and ends it with its pod's outcome; and it posts webhook runs to their receivers, signed, with a token to
// report with. It reaches the runs, the cluster, the secrets, the object store and the receivers only through its
// ports (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "Where a run
// executes").
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"

	"cobranch.dev/internal/services/jobs"
)

var tracer = otel.Tracer("cobranch.dev/internal/services/runner")

// Run is a queued run the controller claimed: its job's definition as it was queued, and what names it.
type Run struct {
	ID, OrgID, RepoID, JobID uuid.UUID
	Number                   int64
	// Job is the job's name.
	Job               string
	OrgSlug, RepoSlug string
	SHA, Ref          string
	// Event is what triggered the run, as the run recorded it: event.json's data.
	Event json.RawMessage
	// Definition is the job as it was declared when the run was queued.
	Definition jobs.Job
	// QueuedAt is when the run was queued, which a webhook run's tries and timeout count from.
	QueuedAt time.Time
}

// Running is a run that runs: what the controller follows it by.
type Running struct {
	ID, OrgID, RepoID uuid.UUID
	Number            int64
	StartedAt         time.Time
	// Timeout is the timeout its job declared; zero for the installation's default.
	Timeout time.Duration
}

// Store keeps the runs: postgres's.
type Store interface {
	// Claim claims the queued run that waited longest and that no controller holds, until leaseUntil, or answers
	// false when none waits.
	Claim(ctx context.Context, leaseUntil time.Time) (Run, bool, error)
	// Token records the run's token by its hash and hint, until expiresAt, replacing one made before.
	Token(ctx context.Context, runID uuid.UUID, hash []byte, hint string, expiresAt time.Time) error
	// Defer puts a queued run off until until, with why.
	Defer(ctx context.Context, runID uuid.UUID, until time.Time, reason string) error
	// MarkRunning marks a queued run running and its job's annotation In progress; false when it is no longer queued.
	MarkRunning(ctx context.Context, runID uuid.UUID) (bool, error)
	// Finish ends a run that hasn't ended, in state with reason, sets its job's annotation to match and deletes its
	// token; false when it had ended already.
	Finish(ctx context.Context, runID uuid.UUID, state jobs.State, reason string) (bool, error)
	// Running lists the runs of pods and scripts that run.
	Running(ctx context.Context) ([]Running, error)
	// RunningWebhooks lists the webhook runs that run: delivered, waiting for their receivers to report.
	RunningWebhooks(ctx context.Context) ([]Running, error)
	// Runs finds those of ids that are runs: where each stands, and its repository.
	Runs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Standing, error)
	// Organizations answers which of ids are organizations still.
	Organizations(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error)
	// DropTokens deletes the tokens of runs that ended without the controller (a member cancelled them, a newer run
	// superseded them), and the tokens that expired, and answers how many.
	DropTokens(ctx context.Context) (int64, error)
	// Scheduled lists the repositories whose default branch's /cobranch.yaml declares jobs with schedules, with those
	// jobs.
	Scheduled(ctx context.Context) ([]Scheduled, error)
	// QueueSlot queues the run that a slot of the schedule of the repository's job fires, at its default branch's head,
	// unless a controller queued that slot already: false then.
	QueueSlot(ctx context.Context, repoID uuid.UUID, job string, slot time.Time) (bool, error)
	// DropSlots deletes the slots queued before a time, and answers how many.
	DropSlots(ctx context.Context, before time.Time) (int64, error)
}

// Scheduled is a repository whose default branch declares jobs with schedules: those jobs.
type Scheduled struct {
	RepoID uuid.UUID
	Jobs   []jobs.Job
}

// Standing is where a run stands, and its repository.
type Standing struct {
	RepoID uuid.UUID
	State  jobs.State
}

// Secrets hands a principal the values of the secrets it may read: secrets.Service's Receive.
type Secrets interface {
	Receive(ctx context.Context, principalID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID][]byte, error)
}

// Launch is a run as the cluster starts it: its pod, its Secret's data, and how long it may take.
type Launch struct {
	Run Run
	// Pod is the job's pod spec, as JSON; nil for a script, which runs Script in Image.
	Pod    json.RawMessage
	Script string
	Image  string
	// Env is every variable each container gets: Cobranch's, the token among them, and the job's secrets. The
	// cluster keeps it in the run's Secret.
	Env map[string]string
	// Masked are the values the run's logs never show: the token and the secrets.
	Masked   []string
	Checkout bool
	Timeout  time.Duration
}

// Outcome is how a run's pod stands.
type Outcome int

// The outcomes of a run's pod.
const (
	// Pending is a pod that hasn't ended: waiting to be scheduled, pulling its images, or running.
	Pending Outcome = iota
	// Succeeded is a pod whose every container exited 0.
	Succeeded
	// Failed is a pod that ended otherwise; its Reason says how.
	Failed
)

// Pod is how a run's pod stands, as the cluster reports it.
type Pod struct {
	Outcome Outcome
	// Reason is how a failed pod failed: the first container that failed and its exit code, its deadline passed, or the
	// node lost it.
	Reason string
}

// ErrNoRoom is a pod the organization's quota refuses for now: its run waits for others to end.
var ErrNoRoom = errors.New("no room in the organization's quota")

// Cluster runs the pods: the Kubernetes adapter's.
type Cluster interface {
	// Prepare makes the organization's job namespace ready for runs, creating or updating it and what it holds.
	Prepare(ctx context.Context, orgID uuid.UUID) error
	// Start creates the run's Secret and pod in its organization's namespace, unless they are there already; ErrNoRoom
	// when the quota refuses the pod.
	Start(ctx context.Context, l Launch) error
	// Pods reports the organization's runs' pods, by run ID: a run without one has none.
	Pods(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]Pod, error)
	// Logs reads each of the run's pod's containers' logs, at most limit bytes each and with the run's masked values
	// hidden, by container; none for a pod that is gone.
	Logs(ctx context.Context, orgID, runID uuid.UUID, limit int64) (map[string][]byte, error)
	// Remove deletes the run's pod, and its Secret with it.
	Remove(ctx context.Context, orgID, runID uuid.UUID) error
	// Namespaces lists the organizations that have a job namespace.
	Namespaces(ctx context.Context) ([]uuid.UUID, error)
	// Drop deletes the organization's job namespace, and everything in it.
	Drop(ctx context.Context, orgID uuid.UUID) error
}

// Archive keeps ended runs' logs: the object store.
type Archive interface {
	// Put writes a run's container's log.
	Put(ctx context.Context, repoID, runID uuid.UUID, container string, log []byte) error
}

// Settings are the installation's for runs.
type Settings struct {
	// Pods is whether runs execute pods at all: off, a pod's or a script's run fails.
	Pods bool
	// Lease is how long a claim holds a queued run before another controller may take it.
	Lease time.Duration
	// RoomRetry is how long a run the quota refused waits before it is tried again.
	RoomRetry time.Duration
	// DefaultTimeout is a run's timeout when its job names none; MaxTimeout caps every run's, within jobs.MaxTimeout.
	DefaultTimeout, MaxTimeout time.Duration
	// ScriptImage is the image a script runs in when its job names none.
	ScriptImage string
	// RESTURL is the REST API's URL, as runs reach it; GitURL the Git server's.
	RESTURL, GitURL string
	// LogLimit is the most of each container's log the archive keeps, in bytes.
	LogLimit int64
	// Vanished is how long a running run may show no pod before it fails: a new pod takes a moment to show.
	Vanished time.Duration
}

// Service runs the runs.
type Service struct {
	store    Store
	cluster  Cluster
	secrets  Secrets
	archive  Archive
	poster   Poster
	settings Settings
	now      func() time.Time
}

// NewService returns a Service that runs store's runs on cluster, with the secrets secrets hands over, keeping their
// logs in archive, and posts webhook runs with poster.
func NewService(store Store, cluster Cluster, secrets Secrets, archive Archive, poster Poster, settings Settings) *Service {
	return &Service{store: store, cluster: cluster, secrets: secrets, archive: archive, poster: poster, settings: settings, now: time.Now}
}

// SetClock replaces the Service's clock, for tests.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// timeout is how long a run of job may take: its own timeout, or the installation's default, within the installation's
// most and jobs.MaxTimeout.
func (s *Service) timeout(job jobs.Job) time.Duration {
	t := time.Duration(job.Timeout)
	if t <= 0 {
		t = s.settings.DefaultTimeout
	}
	return min(t, s.settings.MaxTimeout, jobs.MaxTimeout)
}
