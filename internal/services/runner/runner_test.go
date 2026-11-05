package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/runner"
	"cobranch.dev/internal/services/secrets"
)

var now = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

// store is a runner.Store in memory, recording what the Service did to its runs.
type store struct {
	queued   []runner.Run
	running  []runner.Running
	webhooks []runner.Running
	standing map[uuid.UUID]runner.Standing
	orgs     []uuid.UUID
	tokens   map[uuid.UUID]time.Time
	events   []string
	fail     error
	// ended is a run a member cancels while its pod is made: MarkRunning finds it no longer queued.
	ended bool
	// scheduled are the repositories with scheduled jobs; QueueSlot fails for the jobs slotFails names.
	scheduled []runner.Scheduled
	slotFails map[string]bool
}

func (s *store) Claim(_ context.Context, leaseUntil time.Time) (runner.Run, bool, error) {
	if s.fail != nil || len(s.queued) == 0 {
		return runner.Run{}, false, s.fail
	}
	r := s.queued[0]
	s.queued = s.queued[1:]
	s.events = append(s.events, fmt.Sprintf("claimed #%d until %s", r.Number, leaseUntil.Format(time.TimeOnly)))
	return r, true, nil
}

func (s *store) Token(_ context.Context, runID uuid.UUID, hash []byte, hint string, expiresAt time.Time) error {
	if len(hash) == 0 || hint == "" {
		return errors.New("no token")
	}
	s.tokens[runID] = expiresAt
	return nil
}

func (s *store) Defer(_ context.Context, _ uuid.UUID, until time.Time, reason string) error {
	s.events = append(s.events, fmt.Sprintf("deferred until %s: %s", until.Format(time.TimeOnly), reason))
	return nil
}

func (s *store) MarkRunning(context.Context, uuid.UUID) (bool, error) {
	if s.ended {
		return false, nil
	}
	s.events = append(s.events, "running")
	return true, nil
}

func (s *store) Finish(_ context.Context, _ uuid.UUID, state jobs.State, reason string) (bool, error) {
	s.events = append(s.events, fmt.Sprintf("%s: %s", state, reason))
	return true, nil
}

func (s *store) Running(context.Context) ([]runner.Running, error) { return s.running, s.fail }

func (s *store) RunningWebhooks(context.Context) ([]runner.Running, error) { return s.webhooks, s.fail }

func (s *store) Runs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]runner.Standing, error) {
	out := map[uuid.UUID]runner.Standing{}
	for _, id := range ids {
		if st, ok := s.standing[id]; ok {
			out[id] = st
		}
	}
	return out, nil
}

func (s *store) Organizations(_ context.Context, ids []uuid.UUID) ([]uuid.UUID, error) {
	return slices.DeleteFunc(slices.Clone(ids), func(id uuid.UUID) bool { return !slices.Contains(s.orgs, id) }), nil
}

func (s *store) DropTokens(context.Context) (int64, error) {
	s.events = append(s.events, "dropped dead tokens")
	return 2, nil
}

func (s *store) Scheduled(context.Context) ([]runner.Scheduled, error) {
	return s.scheduled, s.fail
}

func (s *store) QueueSlot(_ context.Context, repoID uuid.UUID, job string, slot time.Time) (bool, error) {
	key := fmt.Sprintf("%s/%s@%s", repoID.String()[:4], job, slot.Format("15:04"))
	if s.slotFails[job] {
		return false, errors.New("the database refused the slot")
	}
	if slices.Contains(s.events, "queued "+key) {
		return false, nil
	}
	s.events = append(s.events, "queued "+key)
	return true, nil
}

func (s *store) DropSlots(_ context.Context, before time.Time) (int64, error) {
	s.events = append(s.events, "dropped slots before "+before.Format(time.DateTime))
	return 1, nil
}

func (s *store) KeepSteps(_ context.Context, runID uuid.UUID, steps []jobs.Step) error {
	kept := make([]string, 0, len(steps))
	for _, step := range steps {
		k := step.Name + " " + string(step.State)
		if step.Reason != "" {
			k += " (" + step.Reason + ")"
		}
		if step.EndedAt != nil {
			k += " until " + step.EndedAt.Format("15:04")
		}
		kept = append(kept, k)
	}
	s.events = append(s.events, "kept "+runID.String()[:4]+"'s steps: "+strings.Join(kept, ", "))
	return nil
}

// cluster is a runner.Cluster in memory: each organization's runs' pods, and what the Service asked of it.
type cluster struct {
	pods     map[uuid.UUID]map[uuid.UUID]runner.Pod
	started  []runner.Launch
	startErr error
	podsErr  map[uuid.UUID]error
	events   []string
}

func (c *cluster) Prepare(_ context.Context, orgID uuid.UUID) error {
	c.events = append(c.events, "prepared "+orgID.String()[:4])
	return nil
}

func (c *cluster) Start(_ context.Context, l runner.Launch) error {
	if c.startErr != nil {
		return c.startErr
	}
	c.started = append(c.started, l)
	return nil
}

func (c *cluster) Pods(_ context.Context, orgID uuid.UUID) (map[uuid.UUID]runner.Pod, error) {
	return c.pods[orgID], c.podsErr[orgID]
}

func (c *cluster) Logs(_ context.Context, _, runID uuid.UUID, limit int64) (map[string][]byte, error) {
	return map[string][]byte{"main": []byte(fmt.Sprintf("log of %s, at most %d", runID.String()[:4], limit))}, nil
}

func (c *cluster) Remove(_ context.Context, _, runID uuid.UUID) error {
	c.events = append(c.events, "removed "+runID.String()[:4])
	return nil
}

func (c *cluster) Namespaces(context.Context) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for id := range c.pods {
		out = append(out, id)
	}
	slices.SortFunc(out, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return out, nil
}

func (c *cluster) Drop(_ context.Context, orgID uuid.UUID) error {
	c.events = append(c.events, "dropped "+orgID.String()[:4])
	return nil
}

// vault is a runner.Secrets that hands over values by ID, refusing those refused lists.
type vault struct {
	values  map[uuid.UUID][]byte
	refused uuid.UUID
	err     error
}

func (v vault) Receive(_ context.Context, _ uuid.UUID, ids []uuid.UUID) (map[uuid.UUID][]byte, error) {
	if v.err != nil {
		return nil, v.err
	}
	out := map[uuid.UUID][]byte{}
	for _, id := range ids {
		if id == v.refused {
			return nil, &secrets.RefusedError{ID: id}
		}
		out[id] = v.values[id]
	}
	return out, nil
}

// poster is a runner.Poster that answers err, recording what it posted.
type poster struct {
	err   error
	posts []post
}

// post is what a poster posted.
type post struct {
	url     string
	body    []byte
	headers map[string]string
}

func (p *poster) Post(_ context.Context, url string, body []byte, headers map[string]string) error {
	p.posts = append(p.posts, post{url: url, body: body, headers: headers})
	return p.err
}

// archive is a runner.Archive in memory.
type archive map[string]string

func (a archive) Put(_ context.Context, _, runID uuid.UUID, container string, log []byte) error {
	a[runID.String()[:4]+"/"+container] = string(log)
	return nil
}

var settings = runner.Settings{Pods: true, Lease: 2 * time.Minute, RoomRetry: 30 * time.Second, DefaultTimeout: time.Hour,
	MaxTimeout: 6 * time.Hour, ScriptImage: "bash:5", RESTURL: "https://api.example.com/v1", GitURL: "https://git.example.com",
	LogLimit: 1024, Vanished: time.Minute}

// id is a UUID that starts with prefix, a 4-character hex string, so the events name it.
func id(prefix string) uuid.UUID {
	return uuid.MustParse(prefix + "0000-0000-7000-8000-000000000000")
}

func scriptRun(change func(*jobs.Job)) runner.Run {
	job := jobs.Job{Name: "ci/build", Script: "make", Permissions: jobs.DefaultPermissions, Checkout: true,
		Secrets: map[string]uuid.UUID{"API_TOKEN": id("5ec1")}}
	if change != nil {
		change(&job)
	}
	return runner.Run{ID: id("aaaa"), OrgID: id("0001"), RepoID: id("0002"), JobID: id("70b0"), Number: 7, Job: job.Name,
		OrgSlug: "acme", RepoSlug: "api", SHA: "abc123", Ref: "refs/heads/main", Event: json.RawMessage(`{"kind":"push"}`),
		Definition: job}
}

func TestDispatch(t *testing.T) {
	tests := []struct {
		name       string
		queued     []runner.Run
		settings   func(*runner.Settings)
		vault      vault
		startErr   error
		ended      bool
		wantOK     bool
		wantErr    string
		wantEvents []string
		wantImage  string
		wantRemove bool
	}{
		{name: "nothing queued", wantEvents: nil},
		{name: "a script", queued: []runner.Run{scriptRun(nil)}, wantOK: true, wantImage: "bash:5",
			wantEvents: []string{"claimed #7 until 09:02:00", "running"}},
		{name: "a script in its own image", queued: []runner.Run{scriptRun(func(j *jobs.Job) { j.Image = "golang:1.27" })}, wantOK: true,
			wantImage: "golang:1.27", wantEvents: []string{"claimed #7 until 09:02:00", "running"}},
		{name: "an installation that runs no pods", queued: []runner.Run{scriptRun(nil)}, settings: func(s *runner.Settings) { s.Pods = false },
			wantOK: true, wantEvents: []string{"claimed #7 until 09:02:00", "failed: this installation runs no pods"}},
		{name: "a pod jobs may not use", queued: []runner.Run{scriptRun(func(j *jobs.Job) {
			j.Script, j.Pod = "", json.RawMessage(`{"hostNetwork":true,"containers":[{"name":"a","image":"b"}]}`)
		})}, wantOK: true, wantEvents: []string{"claimed #7 until 09:02:00", "failed: its pod: a job's pod may not set hostNetwork"}},
		{name: "a checkout without Git over HTTPS", queued: []runner.Run{scriptRun(nil)}, settings: func(s *runner.Settings) { s.GitURL = "" },
			wantOK: true, wantEvents: []string{"claimed #7 until 09:02:00", "failed: this installation serves no Git over HTTPS to check its commit out from"}},
		{name: "a secret its job may not read", queued: []runner.Run{scriptRun(nil)}, vault: vault{refused: id("5ec1")}, wantOK: true,
			wantEvents: []string{"claimed #7 until 09:02:00", "failed: job ci/build may not read secret 5ec10000-0000-7000-8000-000000000000: grant it Secret reader on it or on the repository"}},
		{name: "no secrets key", queued: []runner.Run{scriptRun(nil)}, vault: vault{err: secrets.ErrNoSecretsKey}, wantOK: true,
			wantEvents: []string{"claimed #7 until 09:02:00", "failed: this installation has no secrets key to open its secrets with"}},
		{name: "no room", queued: []runner.Run{scriptRun(nil)}, startErr: fmt.Errorf("pod: %w", runner.ErrNoRoom), wantOK: true,
			wantEvents: []string{"claimed #7 until 09:02:00", "deferred until 09:00:30: waits for room in the organization's quota"}},
		{name: "a pod the cluster refuses", queued: []runner.Run{scriptRun(nil)}, startErr: fmt.Errorf("%w: image is empty", runner.ErrRefused),
			wantOK: true, wantEvents: []string{"claimed #7 until 09:02:00", "failed: the cluster refused the run's pod: image is empty"}},
		{name: "a cluster that fails", queued: []runner.Run{scriptRun(nil)}, startErr: errors.New("connection refused"), wantOK: true,
			wantErr: "starting run aaaa0000-0000-7000-8000-000000000000's pod: connection refused", wantEvents: []string{"claimed #7 until 09:02:00"}},
		{name: "a run cancelled while its pod was made", queued: []runner.Run{scriptRun(nil)}, ended: true, wantOK: true,
			wantEvents: []string{"claimed #7 until 09:02:00"}, wantRemove: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &store{queued: tt.queued, tokens: map[uuid.UUID]time.Time{}, ended: tt.ended}
			c := &cluster{startErr: tt.startErr}
			set := settings
			if tt.settings != nil {
				tt.settings(&set)
			}
			if tt.vault.values == nil {
				tt.vault.values = map[uuid.UUID][]byte{id("5ec1"): []byte("s3cret")}
			}
			svc := runner.NewService(s, c, tt.vault, archive{}, &poster{}, set)
			svc.SetClock(func() time.Time { return now })
			ok, err := svc.Dispatch(context.Background())
			if ok != tt.wantOK || (err == nil) != (tt.wantErr == "") || (err != nil && err.Error() != tt.wantErr) {
				t.Fatalf("Dispatch() = %t, %v, want %t, %q", ok, err, tt.wantOK, tt.wantErr)
			}
			checkDispatched(t, s, c, tt.wantEvents, tt.wantImage, tt.wantRemove)
		})
	}
}

// checkDispatched checks what Dispatch did to the store's runs, the run it started in image, if any, and whether it
// removed that run's pod.
func checkDispatched(t *testing.T, s *store, c *cluster, events []string, image string, remove bool) {
	t.Helper()
	if !slices.Equal(s.events, events) {
		t.Errorf("events = %q, want %q", s.events, events)
	}
	if image != "" {
		checkLaunch(t, c, s, image)
	}
	if removed := slices.Contains(c.events, "removed aaaa"); removed != remove {
		t.Errorf("removed the pod: %t, want %t", removed, remove)
	}
}

// checkLaunch checks the one run c started: its image, Cobranch's variables and the job's secret, the values its logs
// mask, and its token, which lasts the run's timeout and a while to start in.
func checkLaunch(t *testing.T, c *cluster, s *store, image string) {
	t.Helper()
	if len(c.started) != 1 {
		t.Fatalf("started %d runs, want 1", len(c.started))
	}
	l := c.started[0]
	token := l.Env["COBRANCH_TOKEN"]
	want := map[string]string{"COBRANCH_URL": "https://api.example.com/v1", "COBRANCH_GIT_URL": "https://git.example.com/acme/api.git",
		"COBRANCH_ORG": "acme", "COBRANCH_REPO": "api", "COBRANCH_SHA": "abc123", "COBRANCH_REF": "refs/heads/main", "COBRANCH_JOB": "ci/build",
		"COBRANCH_RUN": "7", "COBRANCH_EVENT": "push", "COBRANCH_TOKEN": token, "API_TOKEN": "s3cret"}
	if l.Image != image || !strings.HasPrefix(token, auth.PrefixRunToken) || !mapsEqual(l.Env, want) {
		t.Errorf("launch = %+v, want image %s and env %v", l, image, want)
	}
	if !slices.Equal(l.Masked, []string{token, "s3cret"}) || l.Timeout != time.Hour || !l.Checkout || l.Script != "make" {
		t.Errorf("launch masks %q, times out after %s, checks out %t, runs %q", l.Masked, l.Timeout, l.Checkout, l.Script)
	}
	if got := s.tokens[l.Run.ID]; !got.Equal(now.Add(time.Hour + 15*time.Minute)) {
		t.Errorf("token expires at %s", got)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func TestTimeouts(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{name: "the installation's default", want: time.Hour},
		{name: "its own", timeout: 10 * time.Minute, want: 10 * time.Minute},
		{name: "beyond the installation's most", timeout: 12 * time.Hour, want: 6 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &store{queued: []runner.Run{scriptRun(func(j *jobs.Job) { j.Timeout = jobs.Duration(tt.timeout) })}, tokens: map[uuid.UUID]time.Time{}}
			c := &cluster{}
			svc := runner.NewService(s, c, vault{values: map[uuid.UUID][]byte{}}, archive{}, &poster{}, settings)
			svc.SetClock(func() time.Time { return now })
			if _, err := svc.Dispatch(context.Background()); err != nil || len(c.started) != 1 || c.started[0].Timeout != tt.want {
				t.Errorf("Dispatch() = %v, started %+v, want a timeout of %s", err, c.started, tt.want)
			}
		})
	}
}

func TestFollow(t *testing.T) {
	org, other := id("0001"), id("0003")
	started := now.Add(-10 * time.Minute)
	running := func(prefix string, orgID uuid.UUID, since time.Time, timeout time.Duration) runner.Running {
		return runner.Running{ID: id(prefix), OrgID: orgID, RepoID: id("0002"), StartedAt: since, Timeout: timeout}
	}
	tests := []struct {
		name        string
		running     []runner.Running
		pods        map[uuid.UUID]map[uuid.UUID]runner.Pod
		podsErr     map[uuid.UUID]error
		wantEvents  []string
		wantCluster []string
		wantLogs    []string
		wantErr     string
	}{
		{name: "a pod that runs", running: []runner.Running{running("aaaa", org, started, 0)},
			pods: map[uuid.UUID]map[uuid.UUID]runner.Pod{org: {id("aaaa"): {Outcome: runner.Pending}}}},
		{name: "a pod that succeeded", running: []runner.Running{running("aaaa", org, started, 0)},
			pods:       map[uuid.UUID]map[uuid.UUID]runner.Pod{org: {id("aaaa"): {Outcome: runner.Succeeded}}},
			wantEvents: []string{"succeeded: succeeded"}, wantCluster: []string{"removed aaaa"}, wantLogs: []string{"aaaa/main"}},
		{name: "a pod that failed, its steps kept", running: []runner.Running{running("aaaa", org, started, 0)},
			pods: map[uuid.UUID]map[uuid.UUID]runner.Pod{org: {id("aaaa"): {Outcome: runner.Failed, Reason: "container main exited 2",
				Steps: []jobs.Step{{Name: "cobranch-checkout", State: jobs.StepSucceeded}, {Name: "main", State: jobs.StepFailed, ExitCode: 2}}}}},
			wantEvents:  []string{"kept aaaa's steps: cobranch-checkout succeeded, main failed", "failed: container main exited 2"},
			wantCluster: []string{"removed aaaa"}, wantLogs: []string{"aaaa/main"}},
		{name: "a pod not there yet", running: []runner.Running{running("aaaa", org, now.Add(-time.Second), 0)}},
		{name: "a pod that vanished", running: []runner.Running{running("aaaa", org, started, 0)},
			wantEvents: []string{"failed: its pod vanished"}, wantCluster: []string{"removed aaaa"}, wantLogs: []string{"aaaa/main"}},
		{name: "a pod that never ran past its timeout", running: []runner.Running{running("aaaa", org, now.Add(-16*time.Minute), 10*time.Minute)},
			pods:       map[uuid.UUID]map[uuid.UUID]runner.Pod{org: {id("aaaa"): {Outcome: runner.Pending}}},
			wantEvents: []string{"failed: timed out after 10m0s without its pod running"}, wantCluster: []string{"removed aaaa"}, wantLogs: []string{"aaaa/main"}},
		{name: "a pod still running past its timeout, its running step stopped", running: []runner.Running{running("aaaa", org, now.Add(-16*time.Minute), 10*time.Minute)},
			pods: map[uuid.UUID]map[uuid.UUID]runner.Pod{org: {id("aaaa"): {Outcome: runner.Pending,
				Steps: []jobs.Step{{Name: "cobranch-checkout", State: jobs.StepSucceeded}, {Name: "main", State: jobs.StepRunning, StartedAt: &started}}}}},
			wantEvents: []string{"kept aaaa's steps: cobranch-checkout succeeded, main failed (Stopped when its run ended) until 09:00",
				"failed: timed out after 10m0s without its pod running"}, wantCluster: []string{"removed aaaa"}, wantLogs: []string{"aaaa/main"}},
		{name: "a pod within its timeout's slack", running: []runner.Running{running("aaaa", org, now.Add(-14*time.Minute), 10*time.Minute)},
			pods: map[uuid.UUID]map[uuid.UUID]runner.Pod{org: {id("aaaa"): {Outcome: runner.Pending}}}},
		{name: "an organization whose pods can't be listed", running: []runner.Running{running("aaaa", org, started, 0), running("bbbb", other, started, 0)},
			pods:    map[uuid.UUID]map[uuid.UUID]runner.Pod{other: {id("bbbb"): {Outcome: runner.Succeeded}}},
			podsErr: map[uuid.UUID]error{org: errors.New("forbidden")}, wantEvents: []string{"succeeded: succeeded"},
			wantCluster: []string{"removed bbbb"}, wantLogs: []string{"bbbb/main"},
			wantErr: "listing organization 00010000-0000-7000-8000-000000000000's pods: forbidden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &store{running: tt.running}
			c := &cluster{pods: tt.pods, podsErr: tt.podsErr}
			a := archive{}
			svc := runner.NewService(s, c, vault{}, a, &poster{}, settings)
			svc.SetClock(func() time.Time { return now })
			err := svc.Follow(context.Background())
			if (err == nil) != (tt.wantErr == "") || (err != nil && err.Error() != tt.wantErr) {
				t.Fatalf("Follow() = %v, want %q", err, tt.wantErr)
			}
			checkSwept(t, s, c, a, tt.wantEvents, tt.wantCluster, tt.wantLogs)
		})
	}
}

// checkSwept checks what the store and the cluster were asked, and which logs the archive kept.
func checkSwept(t *testing.T, s *store, c *cluster, a archive, events, clusterEvents, logs []string) {
	t.Helper()
	if !slices.Equal(s.events, events) {
		t.Errorf("store events = %q, want %q", s.events, events)
	}
	if !slices.Equal(c.events, clusterEvents) {
		t.Errorf("cluster events = %q, want %q", c.events, clusterEvents)
	}
	var kept []string
	for k := range a {
		kept = append(kept, k)
	}
	slices.Sort(kept)
	if !slices.Equal(kept, logs) {
		t.Errorf("kept logs %q, want %q", kept, logs)
	}
}

func TestSweep(t *testing.T) {
	org, gone := id("0001"), id("0009")
	pods := func() map[uuid.UUID]map[uuid.UUID]runner.Pod {
		return map[uuid.UUID]map[uuid.UUID]runner.Pod{
			org: {id("aaaa"): {Outcome: runner.Pending}, id("bbbb"): {Outcome: runner.Pending, Steps: []jobs.Step{{Name: "main", State: jobs.StepRunning}}},
				id("cccc"): {}, id("dddd"): {}},
			gone: {id("eeee"): {}},
		}
	}
	standing := map[uuid.UUID]runner.Standing{
		id("aaaa"): {RepoID: id("0002"), State: jobs.StateRunning},
		id("bbbb"): {RepoID: id("0002"), State: jobs.StateCancelled},
		id("cccc"): {RepoID: id("0002"), State: jobs.StateQueued},
	}
	s := &store{standing: standing, orgs: []uuid.UUID{org}}
	c := &cluster{pods: pods()}
	a := archive{}
	svc := runner.NewService(s, c, vault{}, a, &poster{}, settings)
	svc.SetClock(func() time.Time { return now })
	if err := svc.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() = %v", err)
	}
	// A cancelled run's pod goes, its steps and logs kept; a pod with no run goes; a running and a queued run's stay; the
	// gone organization's namespace goes.
	wantCluster := []string{"removed bbbb", "removed dddd", "dropped 0009"}
	slices.Sort(c.events)
	slices.Sort(wantCluster)
	checkSwept(t, s, c, a, []string{"dropped dead tokens", "dropped slots before 2026-11-01 09:00:00", "kept bbbb's steps: main failed (Stopped when its run ended) until 09:00"}, wantCluster,
		[]string{"bbbb/main"})
}

// TestSweepStopsSteps keeps the steps of a run cancelled while its pod ran as they ended: one still running fails when
// the run ended, or when it started if that was later, or now when the run keeps no end; the rest stay as they were.
func TestSweepStopsSteps(t *testing.T) {
	org, run := id("0001"), id("bbbb")
	at := func(hour, minute int) *time.Time {
		at := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, time.UTC)
		return &at
	}
	tests := []struct {
		name  string
		ended *time.Time
		steps []jobs.Step
		want  string
	}{
		{name: "a step still running ends when its run did", ended: at(8, 55),
			steps: []jobs.Step{{Name: "cobranch-checkout", State: jobs.StepSucceeded, EndedAt: at(8, 51)},
				{Name: "main", State: jobs.StepRunning, StartedAt: at(8, 51)}, {Name: "after", State: jobs.StepWaiting}},
			want: "cobranch-checkout succeeded until 08:51, main failed (Stopped when its run ended) until 08:55, after waiting"},
		{name: "a step that started after its run ended ends as it started", ended: at(8, 55),
			steps: []jobs.Step{{Name: "main", State: jobs.StepRunning, StartedAt: at(8, 57)}},
			want:  "main failed (Stopped when its run ended) until 08:57"},
		{name: "a run that keeps no end stops its steps now",
			steps: []jobs.Step{{Name: "main", State: jobs.StepRunning, StartedAt: at(8, 51)}},
			want:  "main failed (Stopped when its run ended) until 09:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &store{standing: map[uuid.UUID]runner.Standing{run: {RepoID: id("0002"), State: jobs.StateCancelled, EndedAt: tt.ended}}, orgs: []uuid.UUID{org}}
			c := &cluster{pods: map[uuid.UUID]map[uuid.UUID]runner.Pod{org: {run: {Outcome: runner.Pending, Steps: tt.steps}}}}
			a := archive{}
			svc := runner.NewService(s, c, vault{}, a, &poster{}, settings)
			svc.SetClock(func() time.Time { return now })
			if err := svc.Sweep(context.Background()); err != nil {
				t.Fatalf("Sweep() = %v", err)
			}
			checkSwept(t, s, c, a, []string{"dropped dead tokens", "dropped slots before 2026-11-01 09:00:00", "kept bbbb's steps: " + tt.want},
				[]string{"removed bbbb"}, []string{"bbbb/main"})
		})
	}
}

func TestSchedule(t *testing.T) {
	repo, other := id("0002"), id("0003")
	nightly := jobs.Job{Name: "nightly", On: jobs.Triggers{Schedule: []jobs.Schedule{{Cron: "55 8 * * *"}}}}
	hourly := jobs.Job{Name: "hourly", On: jobs.Triggers{Schedule: []jobs.Schedule{{Cron: "0 * * * *"}}}}
	weekly := jobs.Job{Name: "weekly", On: jobs.Triggers{Schedule: []jobs.Schedule{{Cron: "0 9 * * 0"}}}}
	broken := jobs.Job{Name: "broken", On: jobs.Triggers{Schedule: []jobs.Schedule{{Cron: "0 * * * *"}}}}
	tests := []struct {
		name    string
		at      time.Time
		passes  int
		fails   map[string]bool
		want    []string
		wantErr bool
	}{
		{name: "the slots due, each once over passes", at: now, passes: 2,
			want: []string{"queued 0002/nightly@08:55", "queued 0002/hourly@09:00", "queued 0003/hourly@09:00"}},
		{name: "a slot past the window", at: now.Add(11 * time.Minute), passes: 1, want: nil},
		{name: "a slot that fails doesn't hold the rest", at: now, passes: 1, fails: map[string]bool{"broken": true},
			want: []string{"queued 0002/nightly@08:55", "queued 0002/hourly@09:00", "queued 0003/hourly@09:00"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheduled := []runner.Scheduled{{RepoID: repo, Jobs: []jobs.Job{nightly, hourly, weekly}}, {RepoID: other, Jobs: []jobs.Job{hourly}}}
			if tt.fails != nil {
				scheduled = append(scheduled, runner.Scheduled{RepoID: id("0004"), Jobs: []jobs.Job{broken}})
			}
			s := &store{scheduled: scheduled, slotFails: tt.fails}
			svc := runner.NewService(s, &cluster{}, vault{}, archive{}, &poster{}, settings)
			svc.SetClock(func() time.Time { return tt.at })
			var err error
			for range tt.passes {
				err = errors.Join(err, svc.Schedule(context.Background()))
			}
			if (err != nil) != tt.wantErr || !slices.Equal(s.events, tt.want) {
				t.Errorf("Schedule() = %v, %q, want error %v, %q", err, s.events, tt.wantErr, tt.want)
			}
		})
	}
}

// TestNoPods runs an installation without pods: its controller fails a queued run of a script, still delivers a
// webhook, fails a run left running when pods went off once its pod counts as vanished, and sweeps without a cluster.
func TestNoPods(t *testing.T) {
	off := settings
	off.Pods = false
	tests := []struct {
		name       string
		store      *store
		step       func(*runner.Service) error
		wantEvents []string
	}{
		{name: "a queued script", store: &store{queued: []runner.Run{scriptRun(nil)}, tokens: map[uuid.UUID]time.Time{}},
			step: func(svc *runner.Service) error {
				_, err := svc.Dispatch(context.Background())
				return err //nolint:wrapcheck // the error under test
			},
			wantEvents: []string{"claimed #7 until 09:02:00", "failed: this installation runs no pods"}},
		{name: "a queued webhook", store: &store{queued: []runner.Run{webhookRun(0)}, tokens: map[uuid.UUID]time.Time{}},
			step: func(svc *runner.Service) error {
				_, err := svc.Dispatch(context.Background())
				return err //nolint:wrapcheck // the error under test
			},
			wantEvents: []string{"claimed #7 until 09:02:00", "running"}},
		{name: "a run still running", store: &store{running: []runner.Running{{ID: id("aaaa"), OrgID: id("0001"), RepoID: id("0002"),
			StartedAt: now.Add(-10 * time.Minute)}}},
			step:       func(svc *runner.Service) error { return svc.Follow(context.Background()) },
			wantEvents: []string{"failed: its pod vanished"}},
		{name: "a sweep", store: &store{}, step: func(svc *runner.Service) error { return svc.Sweep(context.Background()) },
			wantEvents: []string{"dropped dead tokens", "dropped slots before 2026-11-01 09:00:00"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := runner.NewService(tt.store, runner.NoPods{}, vault{values: map[uuid.UUID][]byte{id("5160"): []byte("signing-key")}}, archive{}, &poster{}, off)
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
