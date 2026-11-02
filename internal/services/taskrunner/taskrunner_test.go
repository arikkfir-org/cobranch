package taskrunner_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/runner"
	"cobranch.dev/internal/services/secrets"
	"cobranch.dev/internal/services/taskrunner"
)

var now = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

// id is a UUID that starts with prefix, a 4-character hex string, so the events name it.
func id(prefix string) uuid.UUID {
	return uuid.MustParse(prefix + "0000-0000-7000-8000-000000000000")
}

// store is a taskrunner.Store in memory, recording what the Service did to its tasks.
type store struct {
	queued []taskrunner.Task
	tasks  map[uuid.UUID]taskrunner.Task
	active []taskrunner.Active
	states map[uuid.UUID]agents.TaskState
	tokens map[uuid.UUID]time.Time
	events []string
	// ended is a task a member cancels while its pod is made: MarkRunning finds it no longer queued.
	ended bool
	// restarting is the pod of each task whose restart a controller leased.
	restarting map[uuid.UUID]int
}

func (s *store) Claim(_ context.Context, leaseUntil time.Time) (taskrunner.Task, bool, error) {
	if len(s.queued) == 0 {
		return taskrunner.Task{}, false, nil
	}
	t := s.queued[0]
	s.queued = s.queued[1:]
	s.events = append(s.events, fmt.Sprintf("claimed #%d until %s", t.Number, leaseUntil.Format(time.TimeOnly)))
	return t, true, nil
}

func (s *store) Task(_ context.Context, taskID uuid.UUID) (taskrunner.Task, error) {
	t, ok := s.tasks[taskID]
	if !ok {
		return taskrunner.Task{}, orgs.ErrNotFound
	}
	return t, nil
}

func (s *store) Token(_ context.Context, taskID uuid.UUID, hash []byte, expiresAt time.Time) error {
	if len(hash) == 0 {
		return errors.New("no token")
	}
	s.tokens[taskID] = expiresAt
	return nil
}

func (s *store) Defer(_ context.Context, _ uuid.UUID, until time.Time) error {
	s.events = append(s.events, "deferred until "+until.Format(time.TimeOnly))
	return nil
}

func (s *store) MarkRunning(context.Context, uuid.UUID) (bool, error) {
	if s.ended {
		return false, nil
	}
	s.events = append(s.events, "running")
	return true, nil
}

func (s *store) LeaseRestart(_ context.Context, taskID uuid.UUID, pod int, _ time.Time) (bool, error) {
	if s.restarting[taskID] >= pod {
		return false, nil
	}
	if s.restarting == nil {
		s.restarting = map[uuid.UUID]int{}
	}
	s.restarting[taskID] = pod
	return true, nil
}

func (s *store) Started(_ context.Context, taskID uuid.UUID, pod int) error {
	s.events = append(s.events, fmt.Sprintf("%s's pod %d started", taskID.String()[:4], pod))
	return nil
}

func (s *store) Fail(_ context.Context, _ uuid.UUID, reason string) (bool, error) {
	s.events = append(s.events, "failed: "+reason)
	return true, nil
}

func (s *store) Active(context.Context) ([]taskrunner.Active, error) { return s.active, nil }

func (s *store) States(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]agents.TaskState, error) {
	out := map[uuid.UUID]agents.TaskState{}
	for _, id := range ids {
		if state, ok := s.states[id]; ok {
			out[id] = state
		}
	}
	return out, nil
}

// cluster is a taskrunner.Cluster in memory: each organization's tasks' newest pods, and what the Service asked of it.
type cluster struct {
	pods     map[uuid.UUID]map[uuid.UUID]taskrunner.Pod
	podsErr  map[uuid.UUID]error
	started  []taskrunner.Launch
	startErr error
	events   []string
}

func (c *cluster) Prepare(_ context.Context, orgID uuid.UUID) error {
	c.events = append(c.events, "prepared "+orgID.String()[:4])
	return nil
}

func (c *cluster) StartTask(_ context.Context, l taskrunner.Launch) error {
	if c.startErr != nil {
		return c.startErr
	}
	c.started = append(c.started, l)
	c.events = append(c.events, fmt.Sprintf("started %s/%d", l.Task.ID.String()[:4], l.Pod))
	return nil
}

func (c *cluster) TaskPods(_ context.Context, orgID uuid.UUID) (map[uuid.UUID]taskrunner.Pod, error) {
	return c.pods[orgID], c.podsErr[orgID]
}

func (c *cluster) Renew(_ context.Context, _, taskID uuid.UUID, pod int, token string) error {
	if !strings.HasPrefix(token, auth.PrefixTaskToken) {
		return errors.New("not a task token")
	}
	c.events = append(c.events, fmt.Sprintf("renewed %s/%d", taskID.String()[:4], pod))
	return nil
}

func (c *cluster) RemoveTask(_ context.Context, _, taskID uuid.UUID) error {
	c.events = append(c.events, "removed "+taskID.String()[:4])
	return nil
}

func (c *cluster) Namespaces(context.Context) ([]uuid.UUID, error) {
	return slices.SortedFunc(maps.Keys(c.pods), func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) }), nil
}

// vault is a taskrunner.Secrets that hands over values by ID, refusing refused, or answering it missing.
type vault struct {
	refused uuid.UUID
	missing bool
	err     error
}

func (v vault) Receive(_ context.Context, principalID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID][]byte, error) {
	if v.err != nil {
		return nil, v.err
	}
	if principalID != id("a9e0") {
		return nil, errors.New("not the task's agent")
	}
	values := map[uuid.UUID][]byte{id("6e70"): []byte("provider-key"), id("5ec1"): []byte("s3cret")}
	out := map[uuid.UUID][]byte{}
	for _, id := range ids {
		if id == v.refused {
			return nil, &secrets.RefusedError{ID: id, Missing: v.missing}
		}
		out[id] = values[id]
	}
	return out, nil
}

var settings = taskrunner.Settings{Pods: true, Lease: 2 * time.Minute, RoomRetry: 30 * time.Second, Timeout: 24 * time.Hour,
	Vanished: time.Minute, RESTURL: "https://api.example.com/v1", GitURL: "https://git.example.com", MaxTurns: 200,
	ShellTimeout: 10 * time.Minute, MaxTokens: 8192}

func task(change func(*taskrunner.Task)) taskrunner.Task {
	t := taskrunner.Task{ID: id("aaaa"), OrgID: id("0001"), AgentID: id("a9e0"), Number: 7, OrgSlug: "acme", Provider: "anthropic",
		Model: "test-model", KeyID: id("6e70"), Secrets: map[string]uuid.UUID{"API_TOKEN": id("5ec1")}}
	if change != nil {
		change(&t)
	}
	return t
}

func TestDispatch(t *testing.T) {
	claimed := "claimed #7 until 09:02:00"
	tests := []struct {
		name        string
		queued      []taskrunner.Task
		settings    func(*taskrunner.Settings)
		vault       vault
		startErr    error
		ended       bool
		wantOK      bool
		wantErr     string
		wantEvents  []string
		wantCluster []string
	}{
		{name: "nothing queued"},
		{name: "a task", queued: []taskrunner.Task{task(nil)}, wantOK: true, wantEvents: []string{claimed, "running"},
			wantCluster: []string{"prepared 0001", "started aaaa/1"}},
		{name: "an installation that runs no pods", queued: []taskrunner.Task{task(nil)}, settings: func(s *taskrunner.Settings) { s.Pods = false },
			wantOK: true, wantEvents: []string{claimed, "failed: this installation runs no pods"}},
		{name: "an agent without a model", queued: []taskrunner.Task{task(func(t *taskrunner.Task) { t.Provider, t.Model = "", "" })}, wantOK: true,
			wantEvents: []string{claimed, "failed: its agent has no model: an admin picks one"}},
		{name: "a provider without its key", queued: []taskrunner.Task{task(func(t *taskrunner.Task) { t.KeyID = uuid.Nil })}, wantOK: true,
			wantEvents: []string{claimed, "failed: the organization's anthropic provider has no secret with its key"}},
		{name: "a secret its agent may not read", queued: []taskrunner.Task{task(nil)}, vault: vault{refused: id("5ec1")}, wantOK: true,
			wantEvents: []string{claimed, "failed: its agent may not read secret 5ec10000-0000-7000-8000-000000000000: grant it Secret reader on it"}},
		{name: "a secret that is gone", queued: []taskrunner.Task{task(nil)}, vault: vault{refused: id("6e70"), missing: true}, wantOK: true,
			wantEvents: []string{claimed, "failed: secret 6e700000-0000-7000-8000-000000000000 isn't there"}},
		{name: "no secrets key", queued: []taskrunner.Task{task(nil)}, vault: vault{err: secrets.ErrNoSecretsKey}, wantOK: true,
			wantEvents: []string{claimed, "failed: this installation has no secrets key to open its secrets with"}},
		{name: "secrets that can't be read", queued: []taskrunner.Task{task(nil)}, vault: vault{err: errors.New("database down")}, wantOK: true,
			wantErr: "receiving task aaaa0000-0000-7000-8000-000000000000's secrets: database down", wantEvents: []string{claimed}},
		{name: "no room", queued: []taskrunner.Task{task(nil)}, startErr: fmt.Errorf("pod: %w", runner.ErrNoRoom), wantOK: true,
			wantEvents: []string{claimed, "deferred until 09:00:30"}, wantCluster: []string{"prepared 0001"}},
		{name: "a pod the cluster refuses", queued: []taskrunner.Task{task(nil)}, startErr: fmt.Errorf("%w: image is empty", runner.ErrRefused),
			wantOK: true, wantEvents: []string{claimed, "failed: the cluster refused its pod: image is empty"}, wantCluster: []string{"prepared 0001"}},
		{name: "a cluster that fails", queued: []taskrunner.Task{task(nil)}, startErr: errors.New("connection refused"), wantOK: true,
			wantErr: "starting task aaaa0000-0000-7000-8000-000000000000's pod: connection refused", wantEvents: []string{claimed},
			wantCluster: []string{"prepared 0001"}},
		{name: "a task cancelled while its pod was made", queued: []taskrunner.Task{task(nil)}, ended: true, wantOK: true,
			wantEvents: []string{claimed}, wantCluster: []string{"prepared 0001", "started aaaa/1", "removed aaaa"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &store{queued: tt.queued, tokens: map[uuid.UUID]time.Time{}, ended: tt.ended}
			c := &cluster{startErr: tt.startErr}
			set := settings
			if tt.settings != nil {
				tt.settings(&set)
			}
			svc := taskrunner.NewService(s, c, tt.vault, set)
			svc.SetClock(func() time.Time { return now })
			ok, err := svc.Dispatch(context.Background())
			if ok != tt.wantOK || (err == nil) != (tt.wantErr == "") || (err != nil && err.Error() != tt.wantErr) {
				t.Fatalf("Dispatch() = %t, %v, want %t, %q", ok, err, tt.wantOK, tt.wantErr)
			}
			checkEvents(t, s, c, tt.wantEvents, tt.wantCluster)
		})
	}
}

// checkEvents checks what the store and the cluster were asked.
func checkEvents(t *testing.T, s *store, c *cluster, events, clusterEvents []string) {
	t.Helper()
	if !slices.Equal(s.events, events) {
		t.Errorf("store events = %q, want %q", s.events, events)
	}
	if !slices.Equal(c.events, clusterEvents) {
		t.Errorf("cluster events = %q, want %q", c.events, clusterEvents)
	}
}

func TestLaunch(t *testing.T) {
	base := map[string]string{"COBRANCH_REST_URL": "https://api.example.com/v1", "COBRANCH_MODEL_PROVIDER": "anthropic",
		"COBRANCH_MODEL": "test-model", "COBRANCH_MAX_TURNS": "200", "COBRANCH_SHELL_TIMEOUT": "10m0s", "COBRANCH_MODEL_MAX_TOKENS": "8192",
		"API_TOKEN": "s3cret"}
	tests := []struct {
		name     string
		settings func(*taskrunner.Settings)
		wantEnv  map[string]string
	}{
		{name: "with Git over HTTPS", wantEnv: with(base, "COBRANCH_GIT_URL", "https://git.example.com")},
		{name: "without Git over HTTPS", settings: func(s *taskrunner.Settings) { s.GitURL = "" }, wantEnv: base},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &store{queued: []taskrunner.Task{task(nil)}, tokens: map[uuid.UUID]time.Time{}}
			c := &cluster{}
			set := settings
			if tt.settings != nil {
				tt.settings(&set)
			}
			svc := taskrunner.NewService(s, c, vault{}, set)
			svc.SetClock(func() time.Time { return now })
			if _, err := svc.Dispatch(context.Background()); err != nil || len(c.started) != 1 {
				t.Fatalf("Dispatch() = %v, started %d pods, want 1", err, len(c.started))
			}
			l := c.started[0]
			if !maps.Equal(l.Env, tt.wantEnv) {
				t.Errorf("env = %v, want %v", l.Env, tt.wantEnv)
			}
			if string(l.Key) != "provider-key" || l.Pod != 1 || l.Timeout != 24*time.Hour || !strings.HasPrefix(l.Token, auth.PrefixTaskToken) {
				t.Errorf("launch has key %q, pod %d, timeout %s, token %q", l.Key, l.Pod, l.Timeout, l.Token)
			}
			if got := s.tokens[id("aaaa")]; !got.Equal(now.Add(taskrunner.TokenLife)) {
				t.Errorf("token expires at %s, want %s", got, now.Add(taskrunner.TokenLife))
			}
		})
	}
}

func with(m map[string]string, k, v string) map[string]string {
	out := maps.Clone(m)
	out[k] = v
	return out
}

// followCase is a pass of Follow over the active tasks and the pods the cluster reports, and what it did.
type followCase struct {
	name        string
	active      []taskrunner.Active
	pods        map[uuid.UUID]map[uuid.UUID]taskrunner.Pod
	podsErr     map[uuid.UUID]error
	startErr    error
	gone        bool
	restarting  map[uuid.UUID]int
	wantEvents  []string
	wantCluster []string
	wantErr     string
}

// followCases are TestFollow's, at now.
func followCases() []followCase {
	org, other := id("0001"), id("0003")
	running := func(prefix string, orgID uuid.UUID, pod int, change func(*taskrunner.Active)) taskrunner.Active {
		a := taskrunner.Active{ID: id(prefix), OrgID: orgID, Number: 7, State: agents.TaskRunning, StartedAt: now.Add(-time.Hour), Pod: pod,
			PodStartedAt: now.Add(-20 * time.Minute), TokenAt: now.Add(-10 * time.Minute)}
		if change != nil {
			change(&a)
		}
		return a
	}
	pods := func(orgID uuid.UUID, prefix string, pod taskrunner.Pod) map[uuid.UUID]map[uuid.UUID]taskrunner.Pod {
		return map[uuid.UUID]map[uuid.UUID]taskrunner.Pod{orgID: {id(prefix): pod}}
	}
	failed := taskrunner.Pod{Number: 1, Pod: runner.Pod{Outcome: runner.Failed, Reason: "container harness exited 1"}}
	return []followCase{
		{name: "a pod that runs", active: []taskrunner.Active{running("aaaa", org, 1, nil)},
			pods: pods(org, "aaaa", taskrunner.Pod{Number: 1, Pod: runner.Pod{Outcome: runner.Pending}})},
		{name: "a waiting task's pod", active: []taskrunner.Active{running("aaaa", org, 1, func(a *taskrunner.Active) { a.State = agents.TaskWaiting })},
			pods: pods(org, "aaaa", taskrunner.Pod{Number: 1, Pod: runner.Pod{Outcome: runner.Pending}})},
		{name: "a token to renew", active: []taskrunner.Active{running("aaaa", org, 1, func(a *taskrunner.Active) { a.TokenAt = now.Add(-30 * time.Minute) })},
			pods:        pods(org, "aaaa", taskrunner.Pod{Number: 1, Pod: runner.Pod{Outcome: runner.Pending}}),
			wantCluster: []string{"renewed aaaa/1"}},
		{name: "a task past its timeout", active: []taskrunner.Active{running("aaaa", org, 1, func(a *taskrunner.Active) { a.StartedAt = now.Add(-25 * time.Hour) })},
			pods:       pods(org, "aaaa", taskrunner.Pod{Number: 1, Pod: runner.Pod{Outcome: runner.Pending}}),
			wantEvents: []string{"failed: timed out after 24h0m0s"}, wantCluster: []string{"removed aaaa"}},
		{name: "a pod not there yet", active: []taskrunner.Active{running("aaaa", org, 1, func(a *taskrunner.Active) { a.PodStartedAt = now.Add(-10 * time.Second) })}},
		{name: "an older pod while the newest isn't there yet",
			active: []taskrunner.Active{running("aaaa", org, 2, func(a *taskrunner.Active) { a.PodStartedAt = now.Add(-10 * time.Second) })},
			pods:   pods(org, "aaaa", failed)},
		{name: "a pod that vanished", active: []taskrunner.Active{running("aaaa", org, 1, nil)},
			wantEvents: []string{"aaaa's pod 2 started"}, wantCluster: []string{"removed aaaa", "prepared 0001", "started aaaa/2"}},
		{name: "a pod that failed", active: []taskrunner.Active{running("aaaa", org, 1, nil)}, pods: pods(org, "aaaa", failed),
			wantEvents: []string{"aaaa's pod 2 started"}, wantCluster: []string{"removed aaaa", "prepared 0001", "started aaaa/2"}},
		{name: "a pod whose restart another controller holds", active: []taskrunner.Active{running("aaaa", org, 1, nil)}, pods: pods(org, "aaaa", failed),
			restarting: map[uuid.UUID]int{id("aaaa"): 1}},
		{name: "a harness that exited while its task ran", active: []taskrunner.Active{running("aaaa", org, 1, nil)},
			pods:       pods(org, "aaaa", taskrunner.Pod{Number: 1, Pod: runner.Pod{Outcome: runner.Succeeded}}),
			wantEvents: []string{"aaaa's pod 2 started"}, wantCluster: []string{"removed aaaa", "prepared 0001", "started aaaa/2"}},
		{name: "the last pod that failed", active: []taskrunner.Active{running("aaaa", org, taskrunner.MaxPods, nil)},
			pods:       pods(org, "aaaa", taskrunner.Pod{Number: taskrunner.MaxPods, Pod: failed.Pod}),
			wantEvents: []string{"failed: its pod died 4 times; the last time, container harness exited 1"}, wantCluster: []string{"removed aaaa"}},
		{name: "no room for the next pod", active: []taskrunner.Active{running("aaaa", org, 1, nil)}, pods: pods(org, "aaaa", failed),
			startErr: runner.ErrNoRoom, wantCluster: []string{"removed aaaa", "prepared 0001"}},
		{name: "a task that ended meanwhile", active: []taskrunner.Active{running("aaaa", org, 1, nil)}, pods: pods(org, "aaaa", failed), gone: true},
		{name: "an organization whose pods can't be listed", active: []taskrunner.Active{running("aaaa", org, 1, nil), running("bbbb", other, 1, nil)},
			pods: pods(other, "bbbb", failed), podsErr: map[uuid.UUID]error{org: errors.New("forbidden")},
			wantEvents: []string{"bbbb's pod 2 started"}, wantCluster: []string{"removed bbbb", "prepared 0003", "started bbbb/2"},
			wantErr: "listing organization 00010000-0000-7000-8000-000000000000's task pods: forbidden"},
	}
}

func TestFollow(t *testing.T) {
	other := id("0003")
	for _, tt := range followCases() {
		t.Run(tt.name, func(t *testing.T) {
			tasks := map[uuid.UUID]taskrunner.Task{}
			if !tt.gone {
				tasks[id("aaaa")] = task(func(t *taskrunner.Task) { t.Pods = 1 })
				tasks[id("bbbb")] = task(func(t *taskrunner.Task) { t.ID, t.OrgID, t.Pods = id("bbbb"), other, 1 })
			}
			s := &store{active: tt.active, tasks: tasks, tokens: map[uuid.UUID]time.Time{}, restarting: tt.restarting}
			c := &cluster{pods: tt.pods, podsErr: tt.podsErr, startErr: tt.startErr}
			svc := taskrunner.NewService(s, c, vault{}, settings)
			svc.SetClock(func() time.Time { return now })
			err := svc.Follow(context.Background())
			if (err == nil) != (tt.wantErr == "") || (err != nil && err.Error() != tt.wantErr) {
				t.Fatalf("Follow() = %v, want %q", err, tt.wantErr)
			}
			checkEvents(t, s, c, tt.wantEvents, tt.wantCluster)
		})
	}
}

func TestFollowOnTwoControllers(t *testing.T) {
	// Every controller follows every task: two that saw the same pod fail, from the same list of tasks, start its next
	// pod once, the second leaving it to the first.
	org := id("0001")
	active := []taskrunner.Active{{ID: id("aaaa"), OrgID: org, Number: 7, State: agents.TaskRunning, StartedAt: now.Add(-time.Hour), Pod: 1,
		PodStartedAt: now.Add(-20 * time.Minute), TokenAt: now.Add(-10 * time.Minute)}}
	s := &store{active: active, tasks: map[uuid.UUID]taskrunner.Task{id("aaaa"): task(func(t *taskrunner.Task) { t.Pods = 1 })},
		tokens: map[uuid.UUID]time.Time{}}
	c := &cluster{pods: map[uuid.UUID]map[uuid.UUID]taskrunner.Pod{org: {id("aaaa"): {Number: 1,
		Pod: runner.Pod{Outcome: runner.Failed, Reason: "container harness exited 1"}}}}}
	for i := range 2 {
		svc := taskrunner.NewService(s, c, vault{}, settings)
		svc.SetClock(func() time.Time { return now })
		if err := svc.Follow(context.Background()); err != nil {
			t.Fatalf("controller %d: Follow() = %v", i+1, err)
		}
	}
	checkEvents(t, s, c, []string{"aaaa's pod 2 started"}, []string{"removed aaaa", "prepared 0001", "started aaaa/2"})
}

func TestSweep(t *testing.T) {
	org, quiet := id("0001"), id("0002")
	pod := taskrunner.Pod{Number: 1, Pod: runner.Pod{Outcome: runner.Pending}}
	s := &store{states: map[uuid.UUID]agents.TaskState{id("aaaa"): agents.TaskRunning, id("bbbb"): agents.TaskSucceeded,
		id("dddd"): agents.TaskQueued, id("eeee"): agents.TaskWaiting, id("ffff"): agents.TaskCancelled}}
	c := &cluster{pods: map[uuid.UUID]map[uuid.UUID]taskrunner.Pod{
		org:   {id("aaaa"): pod, id("bbbb"): pod, id("cccc"): pod, id("dddd"): pod, id("eeee"): pod, id("ffff"): pod},
		quiet: {},
	}}
	svc := taskrunner.NewService(s, c, vault{}, settings)
	svc.SetClock(func() time.Time { return now })
	if err := svc.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() = %v", err)
	}
	checkEvents(t, s, c, nil, []string{"removed bbbb", "removed cccc", "removed ffff"})
}

// TestNoPods runs an installation without pods: its controller fails a queued task, fails a task left running when
// pods went off once its pod counts as vanished, and sweeps without a cluster.
func TestNoPods(t *testing.T) {
	off := settings
	off.Pods = false
	running := taskrunner.Active{ID: id("aaaa"), OrgID: id("0001"), Number: 7, State: agents.TaskRunning, StartedAt: now.Add(-time.Hour), Pod: 1,
		PodStartedAt: now.Add(-20 * time.Minute), TokenAt: now.Add(-10 * time.Minute)}
	tests := []struct {
		name       string
		store      *store
		step       func(*taskrunner.Service) error
		wantEvents []string
	}{
		{name: "a queued task", store: &store{queued: []taskrunner.Task{task(nil)}, tokens: map[uuid.UUID]time.Time{}},
			step: func(svc *taskrunner.Service) error {
				_, err := svc.Dispatch(context.Background())
				return err //nolint:wrapcheck // the error under test
			},
			wantEvents: []string{"claimed #7 until 09:02:00", "failed: this installation runs no pods"}},
		{name: "a task still running", store: &store{active: []taskrunner.Active{running},
			tasks: map[uuid.UUID]taskrunner.Task{id("aaaa"): task(func(t *taskrunner.Task) { t.Pods = 1 })}, tokens: map[uuid.UUID]time.Time{}},
			step:       func(svc *taskrunner.Service) error { return svc.Follow(context.Background()) },
			wantEvents: []string{"failed: this installation runs no pods"}},
		{name: "a sweep", store: &store{}, step: func(svc *taskrunner.Service) error { return svc.Sweep(context.Background()) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := taskrunner.NewService(tt.store, taskrunner.NoPods{}, vault{}, off)
			svc.SetClock(func() time.Time { return now })
			if err := tt.step(svc); err != nil {
				t.Fatalf("step = %v", err)
			}
			if !slices.Equal(tt.store.events, tt.wantEvents) {
				t.Errorf("events = %q, want %q", tt.store.events, tt.wantEvents)
			}
		})
	}
}
