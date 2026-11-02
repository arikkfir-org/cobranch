// Package taskrunner is the controller's work for agents: it takes the agents' queued tasks and starts each in a pod
// of its organization's job namespace, with its provider's key, its agent's secrets and a token of its own; renews the
// token while the task runs; starts a pod that died again, at most three times; and fails a task whose pods keep dying
// or that outlives its timeout. It reaches the tasks, the cluster and the secrets only through its ports
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md, "The agent's pod").
package taskrunner

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/runner"
)

var tracer = otel.Tracer("cobranch.dev/internal/services/taskrunner")

// The task token's life: issued for an hour, renewed every half hour while its task runs, so a pod always holds one
// with half an hour left.
const (
	TokenLife  = time.Hour
	RenewEvery = 30 * time.Minute
)

// MaxPods is the most pods a task has: its first, and three started again after one died.
const MaxPods = 4

// Task is a task the controller starts a pod for, and what the pod starts from.
type Task struct {
	ID, OrgID, AgentID uuid.UUID
	Number             int64
	OrgSlug            string
	// Provider and Model are its agent's model; empty until an admin picks one.
	Provider, Model string
	// KeyID is the organization secret that holds the provider's key; uuid.Nil when the organization has no such
	// provider any more, or its secret is gone.
	KeyID uuid.UUID
	// Secrets are its agent's secrets, by the environment variable each becomes.
	Secrets map[string]uuid.UUID
	// Pods is how many pods it has had.
	Pods int
}

// Active is a task that runs or waits in a pod the controller started.
type Active struct {
	ID, OrgID uuid.UUID
	Number    int64
	State     agents.TaskState
	// StartedAt is when the task started, which its timeout counts from.
	StartedAt time.Time
	// Pod is the number of its newest pod, from 1, and PodStartedAt when the controller made it.
	Pod          int
	PodStartedAt time.Time
	// TokenAt is when its newest token was issued.
	TokenAt time.Time
}

// Store keeps the tasks: postgres's.
type Store interface {
	// Claim claims the queued task that waited longest and that no controller holds, until leaseUntil, or answers
	// false when none waits.
	Claim(ctx context.Context, leaseUntil time.Time) (Task, bool, error)
	// Task finds a task that runs or waits, to start its pod again; orgs.ErrNotFound when it ended or is gone.
	Task(ctx context.Context, id uuid.UUID) (Task, error)
	// Token records a token of the task by its hash, until expiresAt; orgs.ErrNotFound when the task ended.
	Token(ctx context.Context, taskID uuid.UUID, hash []byte, expiresAt time.Time) error
	// Defer puts a queued task off until until.
	Defer(ctx context.Context, taskID uuid.UUID, until time.Time) error
	// MarkRunning marks a queued task running in its first pod; false when it is no longer queued.
	MarkRunning(ctx context.Context, taskID uuid.UUID) (bool, error)
	// LeaseRestart claims a task whose newest pod is pod, for this controller alone to start its next, until leaseUntil;
	// false when another controller holds it, or started its next pod already.
	LeaseRestart(ctx context.Context, taskID uuid.UUID, pod int, leaseUntil time.Time) (bool, error)
	// Started records that the task's pod numbered pod started now, and ends the lease of its restart.
	Started(ctx context.Context, taskID uuid.UUID, pod int) error
	// Fail ends a task that hasn't ended Failed, for reason, recording the change in its transcript; false when it had
	// ended already.
	Fail(ctx context.Context, taskID uuid.UUID, reason string) (bool, error)
	// Active lists the tasks that run or wait.
	Active(ctx context.Context) ([]Active, error)
	// States answers the states of those of ids that are tasks.
	States(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]agents.TaskState, error)
}

// Secrets hands a principal the values of the secrets it may read: secrets.Service's Receive.
type Secrets = runner.Secrets

// Launch is a task's pod as the cluster starts it.
type Launch struct {
	Task Task
	// Pod is the pod's number, from 1.
	Pod int
	// Token is the task token, which the harness reads from a file the controller renews.
	Token string
	// Key is the provider's API key, which only the pod's model proxy holds: the harness reaches the provider through it.
	Key []byte
	// Env are the harness's variables: Cobranch's, which it keeps from its tools, and the agent's secrets, which its
	// tools get.
	Env map[string]string
	// Timeout is how long the pod may run.
	Timeout time.Duration
}

// Pod is how a task's newest pod stands, as the cluster reports it.
type Pod struct {
	// Number is the pod's number, from 1.
	Number int
	runner.Pod
}

// Cluster runs the tasks' pods: the Kubernetes adapter's.
type Cluster interface {
	// Prepare makes the organization's job namespace ready, as for runs.
	Prepare(ctx context.Context, orgID uuid.UUID) error
	// StartTask creates the task's pod and its Secret in its organization's namespace; runner.ErrNoRoom when the quota
	// refuses it, runner.ErrRefused when the cluster refuses its spec.
	StartTask(ctx context.Context, l Launch) error
	// TaskPods reports the organization's tasks' newest pods, by task ID: a task without one has none.
	TaskPods(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]Pod, error)
	// Renew writes a new token into the Secret of the task's pod numbered pod, which the harness reads.
	Renew(ctx context.Context, orgID, taskID uuid.UUID, pod int, token string) error
	// RemoveTask deletes every pod of the task, and their Secrets with them.
	RemoveTask(ctx context.Context, orgID, taskID uuid.UUID) error
	// Namespaces lists the organizations that have a job namespace.
	Namespaces(ctx context.Context) ([]uuid.UUID, error)
}

// Settings are the installation's for agents' pods.
type Settings struct {
	// Pods is whether the installation runs pods at all: off, a task fails.
	Pods bool
	// Lease is how long a claim holds a queued task; RoomRetry how long a task the quota refused waits.
	Lease, RoomRetry time.Duration
	// Timeout is the longest a task may run.
	Timeout time.Duration
	// Vanished is how long a task's newest pod may not show before the controller counts it dead.
	Vanished time.Duration
	// RESTURL and GitURL are where the harness reaches the REST API and the Git server.
	RESTURL, GitURL string
	// MaxTurns, ShellTimeout and MaxTokens bound the harness: its model's turns, a shell command, and a turn's tokens.
	MaxTurns     int
	ShellTimeout time.Duration
	MaxTokens    int
}

// Service runs the agents' tasks in pods.
type Service struct {
	store    Store
	cluster  Cluster
	secrets  Secrets
	settings Settings
	now      func() time.Time
}

// NewService returns a Service that runs store's tasks on cluster, with the secrets secrets hands over.
func NewService(store Store, cluster Cluster, secrets Secrets, settings Settings) *Service {
	return &Service{store: store, cluster: cluster, secrets: secrets, settings: settings, now: time.Now}
}

// SetClock replaces the Service's clock, for tests.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// taskAttrs are the attributes of a span about a task.
func taskAttrs(taskID, orgID uuid.UUID, number int64) []attribute.KeyValue {
	return []attribute.KeyValue{attribute.String("cobranch.task.id", taskID.String()), attribute.Int64("cobranch.task.number", number),
		attribute.String("cobranch.org.id", orgID.String())}
}

// record marks span failed with err, if any.
func record(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}
