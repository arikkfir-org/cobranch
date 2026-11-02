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
	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/taskrunner"
)

// TaskPodStore keeps the agents' tasks as the controller runs them: taskrunner.Store. Its changes are Cobranch's own,
// so its transactions name nobody, and the events of a task's states name its agent, which works it.
type TaskPodStore struct {
	pool *pgxpool.Pool
	q    *queries.Queries
}

// NewTaskPodStore returns the tasks' store for the controller on pool's database.
func NewTaskPodStore(pool *pgxpool.Pool) *TaskPodStore {
	return &TaskPodStore{pool: pool, q: queries.New(pool)}
}

var _ taskrunner.Store = (*TaskPodStore)(nil)

// startRow is a task to start a pod for, as ClaimTask and TaskToStart answer it.
type startRow struct {
	ID       uuid.UUID
	OrgID    uuid.UUID
	Number   int64
	AgentID  uuid.UUID
	OrgSlug  string
	Provider *string
	Model    *string
	Secrets  []byte
	KeyID    *uuid.UUID
	Attempt  int32
}

// taskToStart is the domain's view of a startRow.
func taskToStart(r startRow) (taskrunner.Task, error) {
	t := taskrunner.Task{ID: r.ID, OrgID: r.OrgID, AgentID: r.AgentID, Number: r.Number, OrgSlug: r.OrgSlug, KeyID: orNil(r.KeyID),
		Pods: int(r.Attempt)}
	if r.Provider != nil && r.Model != nil {
		t.Provider, t.Model = *r.Provider, *r.Model
	}
	if err := json.Unmarshal(r.Secrets, &t.Secrets); err != nil {
		return taskrunner.Task{}, fmt.Errorf("reading task %s's agent's secrets: %w", r.ID, err)
	}
	return t, nil
}

// Claim claims the queued task that waited longest and that no controller holds, until leaseUntil (taskrunner.Store).
func (s *TaskPodStore) Claim(ctx context.Context, leaseUntil time.Time) (taskrunner.Task, bool, error) {
	r, err := s.q.ClaimTask(ctx, leaseUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return taskrunner.Task{}, false, nil
	}
	if err != nil {
		return taskrunner.Task{}, false, fmt.Errorf("claiming a queued task: %w", err)
	}
	t, err := taskToStart(startRow(r))
	return t, err == nil, err
}

// Task finds a task that runs or waits, to start its pod again (taskrunner.Store).
func (s *TaskPodStore) Task(ctx context.Context, id uuid.UUID) (taskrunner.Task, error) {
	r, err := s.q.TaskToStart(ctx, id)
	if err != nil {
		return taskrunner.Task{}, fmt.Errorf("finding task %s: %w", id, notFound(err))
	}
	return taskToStart(startRow(r))
}

// Token records a token of the task by its hash, until expiresAt (taskrunner.Store).
func (s *TaskPodStore) Token(ctx context.Context, taskID uuid.UUID, hash []byte, expiresAt time.Time) error {
	n, err := s.q.InsertTaskToken(ctx, hash, expiresAt, taskID)
	switch {
	case err != nil:
		return fmt.Errorf("recording the task token: %w", err)
	case n == 0:
		return orgs.ErrNotFound
	}
	return nil
}

// Defer puts a queued task off until until (taskrunner.Store).
func (s *TaskPodStore) Defer(ctx context.Context, taskID uuid.UUID, until time.Time) error {
	if err := s.q.DeferTask(ctx, until, taskID); err != nil {
		return fmt.Errorf("putting the task off: %w", err)
	}
	return nil
}

// MarkRunning marks a queued task running in its first pod, which started now (taskrunner.Store).
func (s *TaskPodStore) MarkRunning(ctx context.Context, taskID uuid.UUID) (bool, error) {
	marked := false
	err := transact(ctx, s.pool, s.q, "marking the task running", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		r, err := q.MarkTaskRunning(ctx, taskID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("marking the task running: %w", err)
		}
		if err := q.RecordTaskPod(ctx, 1, taskID); err != nil {
			return fmt.Errorf("recording the task's pod: %w", err)
		}
		marked = true
		return orgEvent(ctx, q, &r.OrgID, r.AgentID, "task.running", taskID, map[string]any{"number": r.Number})
	})
	return marked, err
}

// Started records that the task's pod numbered pod started now (taskrunner.Store).
func (s *TaskPodStore) Started(ctx context.Context, taskID uuid.UUID, pod int) error {
	if err := s.q.RecordTaskPod(ctx, int32(pod), taskID); err != nil { //nolint:gosec // at most taskrunner.MaxPods
		return fmt.Errorf("recording the task's pod: %w", err)
	}
	return nil
}

// Fail ends a task that hasn't ended Failed, for reason, and appends the change to its transcript, which no harness
// writes to any more (taskrunner.Store).
func (s *TaskPodStore) Fail(ctx context.Context, taskID uuid.UUID, reason string) (bool, error) {
	failed := false
	err := transact(ctx, s.pool, s.q, "failing the task", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		r, err := q.FailTask(ctx, reason, taskID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failing the task: %w", err)
		}
		failed = true
		last, err := q.LastEntry(ctx, taskID)
		if err != nil {
			return fmt.Errorf("reading the transcript's last entry: %w", err)
		}
		body, err := json.Marshal(agents.StateChange{State: agents.TaskFailed, Reason: reason})
		if err != nil {
			return fmt.Errorf("encoding the state entry: %w", err)
		}
		if _, err := q.InsertEntry(ctx, queries.InsertEntryParams{TaskID: taskID, Seq: last + 1, Kind: string(agents.EntryState), Body: body}); err != nil {
			return fmt.Errorf("appending the state entry: %w", err)
		}
		return orgEvent(ctx, q, &r.OrgID, r.AgentID, "task.failed", taskID, map[string]any{"number": r.Number})
	})
	return failed, err
}

// Active lists the tasks that run or wait (taskrunner.Store).
func (s *TaskPodStore) Active(ctx context.Context) ([]taskrunner.Active, error) {
	rows, err := s.q.ActiveTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing the running tasks: %w", err)
	}
	out := make([]taskrunner.Active, 0, len(rows))
	for _, r := range rows {
		out = append(out, taskrunner.Active{ID: r.ID, OrgID: r.OrgID, Number: r.Number, State: agents.TaskState(r.State), StartedAt: r.StartedAt,
			Pod: int(r.Attempt), PodStartedAt: r.PodStartedAt, TokenAt: r.TokenAt})
	}
	return out, nil
}

// States answers the states of those of ids that are tasks (taskrunner.Store).
func (s *TaskPodStore) States(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]agents.TaskState, error) {
	rows, err := s.q.TaskStates(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("finding the tasks: %w", err)
	}
	out := make(map[uuid.UUID]agents.TaskState, len(rows))
	for _, r := range rows {
		out[r.ID] = agents.TaskState(r.State)
	}
	return out, nil
}
