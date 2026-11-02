package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/secrets"
	"cobranch.dev/internal/services/taskrunner"
)

// taskPodFixture is Acme with Scout on Anthropic's model, a key for it, and a secret of Scout's.
type taskPodFixture struct {
	*agentFixture
	s          *TaskPodStore
	key, token uuid.UUID
}

func newTaskPodFixture(t *testing.T) *taskPodFixture {
	t.Helper()
	ctx := context.Background()
	f := &taskPodFixture{agentFixture: newAgentFixture(t)}
	f.s = NewTaskPodStore(f.pool)
	f.key, f.token = f.addSecret(t, uuid.Nil, "anthropic-key"), f.addSecret(t, uuid.Nil, "token")
	if _, _, err := f.a.SetModelProvider(ctx, f.olivia, f.acme.ID, "anthropic", f.key, agents.NoVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.SetModel(ctx, f.olivia, f.acme.ID, f.scout.ID, agents.AnyVersion, "anthropic", "test-model"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.SetSecrets(ctx, f.olivia, f.acme.ID, f.scout.ID, agents.AnyVersion, map[string]uuid.UUID{"TOKEN": f.token}); err != nil {
		t.Fatal(err)
	}
	return f
}

// claim claims the next queued task, which must be want.
func (f *taskPodFixture) claim(t *testing.T, want uuid.UUID) taskrunner.Task {
	t.Helper()
	got, ok, err := f.s.Claim(context.Background(), time.Now().Add(2*time.Minute))
	if err != nil || !ok || got.ID != want {
		t.Fatalf("Claim() = %+v, %t, %v, want task %s", got, ok, err, want)
	}
	return got
}

func TestTaskPodStoreClaims(t *testing.T) {
	t.Parallel()
	f := newTaskPodFixture(t)
	ctx := context.Background()
	first, second := f.assigned(t, "Look around", false, uuid.Nil), f.assigned(t, "Look again", false, uuid.Nil)
	got := f.claim(t, first.ID)
	want := taskrunner.Task{ID: first.ID, OrgID: f.acme.ID, AgentID: f.scout.ID, Number: first.Number, OrgSlug: "acme", Provider: "anthropic",
		Model: "test-model", KeyID: f.key}
	if got.ID != want.ID || got.OrgID != want.OrgID || got.AgentID != want.AgentID || got.Number != want.Number || got.OrgSlug != want.OrgSlug ||
		got.Provider != want.Provider || got.Model != want.Model || got.KeyID != want.KeyID || got.Pods != 0 ||
		!maps.Equal(got.Secrets, map[string]uuid.UUID{"TOKEN": f.token}) {
		t.Errorf("Claim() = %+v, want %+v with Scout's secret", got, want)
	}
	f.claim(t, second.ID)
	if got, ok, err := f.s.Claim(ctx, time.Now().Add(2*time.Minute)); err != nil || ok {
		t.Fatalf("Claim() while both are claimed = %+v, %t, %v; want none", got, ok, err)
	}
	if err := f.s.Defer(ctx, first.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	f.claim(t, first.ID)
	if err := NewSecretStore(f.pool).Delete(ctx, f.olivia, f.key, secrets.AnyVersion); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Defer(ctx, second.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := f.claim(t, second.ID); got.KeyID != uuid.Nil || got.Provider != "anthropic" {
		t.Errorf("Claim() once the key is gone = %+v, want no key", got)
	}
}

func TestTaskPodStoreRunsATask(t *testing.T) {
	t.Parallel()
	f := newTaskPodFixture(t)
	ctx := context.Background()
	task, queued := f.assigned(t, "Look around", false, uuid.Nil), f.assigned(t, "Look again", false, uuid.Nil)
	f.claim(t, task.ID)
	if err := f.s.Token(ctx, task.ID, auth.Hash(auth.NewSecret(auth.PrefixTaskToken)), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Token(ctx, uuid.New(), auth.Hash(auth.NewSecret(auth.PrefixTaskToken)), time.Now().Add(time.Hour)); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Token(no task) = %v, want %v", err, orgs.ErrNotFound)
	}
	for i, want := range []bool{true, false} {
		if marked, err := f.s.MarkRunning(ctx, task.ID); err != nil || marked != want {
			t.Fatalf("MarkRunning() #%d = %t, %v, want %t", i+1, marked, err, want)
		}
	}
	f.checkActive(t, task.ID, 1)
	f.checkRestartLease(t, task.ID)
	f.checkActive(t, task.ID, 2)
	if got, err := f.s.Task(ctx, task.ID); err != nil || got.Pods != 2 || got.KeyID != f.key || got.Secrets["TOKEN"] != f.token {
		t.Errorf("Task() = %+v, %v, want its second pod", got, err)
	}
	if _, err := f.s.Task(ctx, queued.ID); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Task(queued) = %v, want %v", err, orgs.ErrNotFound)
	}
	f.checkFail(t, task.ID, queued.ID)
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind IN ('task.running', 'task.failed') AND actor_id = $1", f.scout.ID); n != 3 {
		t.Errorf("task events = %d, want 3", n)
	}
}

// checkRestartLease checks that of the controllers that race to start task's second pod, one leases it, and that its
// next restart waits for the second pod to start; then starts it.
func (f *taskPodFixture) checkRestartLease(t *testing.T, task uuid.UUID) {
	t.Helper()
	ctx, until := context.Background(), time.Now().Add(time.Minute)
	if leased, err := f.s.LeaseRestart(ctx, task, 2, until); err != nil || leased {
		t.Errorf("LeaseRestart(a pod not started) = %t, %v; want false", leased, err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			leased, err := f.s.LeaseRestart(ctx, task, 1, until)
			if err != nil {
				t.Errorf("LeaseRestart() = %v", err)
			}
			if leased {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Errorf("LeaseRestart() leased %d times to controllers racing for it, want once", wins.Load())
	}
	if err := f.s.Started(ctx, task, 2); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		pod  int
		want bool
	}{{pod: 1, want: false}, {pod: 2, want: true}} {
		if leased, err := f.s.LeaseRestart(ctx, task, c.pod, until); err != nil || leased != c.want {
			t.Errorf("LeaseRestart(pod %d) after pod 2 started = %t, %v; want %t", c.pod, leased, err, c.want)
		}
	}
}

// checkActive checks that the store lists task alone as active, with its newest pod numbered pod, which started a
// moment ago, and its token.
func (f *taskPodFixture) checkActive(t *testing.T, task uuid.UUID, pod int) {
	t.Helper()
	active, err := f.s.Active(context.Background())
	if err != nil || len(active) != 1 {
		t.Fatalf("Active() = %+v, %v, want one task", active, err)
	}
	a, recent := active[0], time.Now().Add(-time.Minute)
	if a.ID != task || a.OrgID != f.acme.ID || a.State != agents.TaskRunning || a.Pod != pod || a.StartedAt.Before(recent) ||
		a.PodStartedAt.Before(recent) || a.TokenAt.Before(recent) {
		t.Errorf("Active() = %+v, want task %s in pod %d, started and given a token a moment ago", a, task, pod)
	}
}

// checkFail fails the running task, whose transcript ends with its state, and the queued one, and checks their states.
func (f *taskPodFixture) checkFail(t *testing.T, running, queued uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	for i, want := range []bool{true, false} {
		if failed, err := f.s.Fail(ctx, running, "its pod died 4 times"); err != nil || failed != want {
			t.Fatalf("Fail() #%d = %t, %v, want %t", i+1, failed, err, want)
		}
	}
	if failed, err := f.s.Fail(ctx, queued, "this installation runs no pods"); err != nil || !failed {
		t.Fatalf("Fail(queued) = %t, %v", failed, err)
	}
	var kind, state, reason string
	if err := f.pool.QueryRow(ctx, "SELECT kind, body->>'state', body->>'reason' FROM agent_task_entries WHERE task_id = $1 ORDER BY seq DESC LIMIT 1",
		running).Scan(&kind, &state, &reason); err != nil || kind != string(agents.EntryState) || state != "failed" || reason != "its pod died 4 times" {
		t.Errorf("last entry = %s %s %q, %v; want the failure", kind, state, reason, err)
	}
	states, err := f.s.States(ctx, []uuid.UUID{running, queued, uuid.New()})
	if err != nil || len(states) != 2 || states[running] != agents.TaskFailed || states[queued] != agents.TaskFailed {
		t.Errorf("States() = %v, %v", states, err)
	}
	if active, err := f.s.Active(ctx); err != nil || len(active) != 0 {
		t.Errorf("Active() after it failed = %+v, %v; want none", active, err)
	}
}

func TestTaskPodStoreStopsAWaitingTask(t *testing.T) {
	t.Parallel()
	f := newTaskPodFixture(t)
	ctx := context.Background()
	task := f.assigned(t, "Ask around", true, uuid.Nil)
	f.claim(t, task.ID)
	if _, err := f.s.MarkRunning(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if stopped, err := f.s.Stop(ctx, task.ID, 1); err != nil || stopped {
		t.Errorf("Stop() while the task runs = %t, %v; want false", stopped, err)
	}
	chat := f.agentFixture.s
	thread, _, err := chat.StartThread(ctx, chats.Poster{ID: f.scout.ID, Task: task.ID}, f.feature, v7(), v7(), nil, "Which branch?")
	if err != nil {
		t.Fatal(err)
	}
	waiting := agents.StateChange{State: agents.TaskWaiting}
	if _, _, err := f.a.Append(ctx, f.scout.ID, task.ID, agents.Entry{Seq: 1, Kind: agents.EntryState, Body: json.RawMessage(`{"state":"waiting"}`)}, &waiting); err != nil {
		t.Fatal(err)
	}
	f.checkIdle(t, "once it waits", false, 0, false)
	for i, want := range []bool{true, false} {
		if stopped, err := f.s.Stop(ctx, task.ID, 1); err != nil || stopped != want {
			t.Fatalf("Stop() #%d = %t, %v, want %t", i+1, stopped, err, want)
		}
	}
	f.checkIdle(t, "once its pod stopped", true, 1, false)
	if _, _, err := chat.Post(ctx, chats.Poster{ID: f.scout.ID, Task: task.ID}, f.feature, thread.ID, v7(), "Still here."); err != nil {
		t.Fatal(err)
	}
	f.checkIdle(t, "after its own message", true, 1, false)
	if _, _, err := chat.Post(ctx, chats.Poster{ID: f.olivia}, f.feature, thread.ID, v7(), "Main."); err != nil {
		t.Fatal(err)
	}
	f.checkIdle(t, "after an answer", true, 1, true)
	if err := f.s.Started(ctx, task.ID, 2); err != nil {
		t.Fatal(err)
	}
	f.checkIdle(t, "once its next pod started", false, 1, true)
	if stopped, err := f.s.Stop(ctx, task.ID, 1); err != nil || stopped {
		t.Errorf("Stop() of the pod before = %t, %v; want false, its next pod having taken its place", stopped, err)
	}
	f.checkIdle(t, "after a stop of the pod before", false, 1, true)
}

// checkIdle checks that the store lists the one task as waiting since a moment ago, with its pod stopped or not, the
// stops it had, and an answer or not.
func (f *taskPodFixture) checkIdle(t *testing.T, when string, stopped bool, stops int, answered bool) {
	t.Helper()
	active, err := f.s.Active(context.Background())
	if err != nil || len(active) != 1 {
		t.Fatalf("Active() %s = %+v, %v, want one task", when, active, err)
	}
	a := active[0]
	if a.State != agents.TaskWaiting || a.StateAt.Before(time.Now().Add(-time.Minute)) || a.Stopped != stopped || a.Stops != stops || a.Answered != answered {
		t.Errorf("Active() %s = %+v, want it waiting since a moment ago, stopped %t after %d stops, answered %t", when, a, stopped, stops, answered)
	}
}
