package deliverer_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/deliverer"
	"cobranch.dev/internal/services/runner"
)

var now = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

// world is the deliveries, the job namespaces' render pods and the cluster in memory, and what the Service did to
// them, in order.
type world struct {
	events []string
	// The store's: the queued deliveries, those that work, the applies TakeApply answers, where each stands, the
	// tokens, the Progressing environments and their objects, and what was recorded.
	queued      []deliverer.Delivery
	working     []deliverer.Working
	applies     map[uuid.UUID]deliverer.Apply
	standing    map[uuid.UUID]deliverer.Standing
	tokens      map[uuid.UUID]time.Time
	progressing []deliverer.Environment
	objects     map[uuid.UUID][]deliverer.Object
	recorded    []deliverer.Outcome
	// superseded is a delivery that ended while its pod was made: MarkRendering finds it no longer queued.
	superseded bool
	// The pods': each organization's render pods, what was started, and what starting answers.
	pods     map[uuid.UUID]map[uuid.UUID]runner.Pod
	started  []deliverer.Render
	startErr error
	logs     map[string][]byte
	// The cluster's: what each call answers.
	prepareErr, applyErr, pruneErr, healthErr error
	applied                                   []deliverer.Object
	health                                    code.Health
	healths                                   map[string]code.Health
}

func newWorld() *world {
	return &world{applies: map[uuid.UUID]deliverer.Apply{}, standing: map[uuid.UUID]deliverer.Standing{}, tokens: map[uuid.UUID]time.Time{},
		objects: map[uuid.UUID][]deliverer.Object{}, pods: map[uuid.UUID]map[uuid.UUID]runner.Pod{}, health: code.Healthy,
		logs: map[string][]byte{"checkout": []byte("Checked out.\n"), "render": []byte("Rendering chart.\n")}}
}

func (w *world) log(format string, args ...any) {
	w.events = append(w.events, fmt.Sprintf(format, args...))
}

// store is the world's deliverer.Store.
type store struct{ *world }

func (s store) Claim(_ context.Context, leaseUntil time.Time) (deliverer.Delivery, bool, error) {
	if len(s.queued) == 0 {
		return deliverer.Delivery{}, false, nil
	}
	d := s.queued[0]
	s.queued = s.queued[1:]
	s.log("claimed #%d until %s", d.Number, leaseUntil.Format(time.TimeOnly))
	return d, true, nil
}

func (s store) Token(_ context.Context, deliveryID uuid.UUID, hash []byte, hint string, expiresAt time.Time) error {
	if len(hash) == 0 || hint == "" {
		return errors.New("no delivery token")
	}
	s.tokens[deliveryID] = expiresAt
	return nil
}

func (s store) Defer(_ context.Context, _ uuid.UUID, until time.Time, reason string) error {
	s.log("deferred until %s: %s", until.Format(time.TimeOnly), reason)
	return nil
}

func (s store) MarkRendering(context.Context, uuid.UUID) (bool, error) {
	if s.superseded {
		return false, nil
	}
	s.log("rendering")
	return true, nil
}

func (s store) Working(context.Context) ([]deliverer.Working, error) { return s.working, nil }

func (s store) TakeApply(_ context.Context, deliveryID uuid.UUID, leaseUntil time.Time) (deliverer.Apply, bool, error) {
	a, ok := s.applies[deliveryID]
	if ok {
		s.log("applying #%d until %s", a.Number, leaseUntil.Format(time.TimeOnly))
	}
	return a, ok, nil
}

func (s store) Record(_ context.Context, o deliverer.Outcome) error {
	s.recorded = append(s.recorded, o)
	s.log("recorded %s, %s: %s %q", o.State, o.Health, o.Report.Status, o.Report.Summary)
	return nil
}

func (s store) Finish(_ context.Context, _ uuid.UUID, state code.DeliveryState, reason string) (bool, error) {
	s.log("%s: %s", state, reason)
	return true, nil
}

func (s store) Deliveries(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]deliverer.Standing, error) {
	out := map[uuid.UUID]deliverer.Standing{}
	for _, id := range ids {
		if st, ok := s.standing[id]; ok {
			out[id] = st
		}
	}
	return out, nil
}

func (s store) DropTokens(context.Context) (int64, error) {
	s.log("dropped dead tokens")
	return 1, nil
}

func (s store) Progressing(context.Context) ([]deliverer.Environment, error) {
	return s.progressing, nil
}

func (s store) Objects(_ context.Context, environmentID uuid.UUID) ([]deliverer.Object, error) {
	return s.objects[environmentID], nil
}

func (s store) SetHealth(_ context.Context, environmentID uuid.UUID, _ []deliverer.Object, health code.Health, report code.AnnotationInput) error {
	s.log("environment %s %s: %s %q", environmentID.String()[:4], health, report.Status, report.Summary)
	return nil
}

// pods is the world's deliverer.Pods.
type pods struct{ *world }

func (p pods) Prepare(_ context.Context, orgID uuid.UUID) error {
	p.log("prepared job namespace %s", orgID.String()[:4])
	return nil
}

func (p pods) StartRender(_ context.Context, r deliverer.Render) error {
	if p.startErr != nil {
		return p.startErr
	}
	p.started = append(p.started, r)
	p.log("started render pod")
	return nil
}

func (p pods) Renders(_ context.Context, orgID uuid.UUID) (map[uuid.UUID]runner.Pod, error) {
	return p.pods[orgID], nil
}

func (p pods) RenderLogs(context.Context, uuid.UUID, uuid.UUID, int64) (map[string][]byte, error) {
	return p.logs, nil
}

func (p pods) RemoveRender(_ context.Context, _, deliveryID uuid.UUID) error {
	p.log("removed render pod %s", deliveryID.String()[:4])
	return nil
}

func (p pods) Namespaces(context.Context) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(p.pods))
	for id := range p.pods {
		out = append(out, id)
	}
	slices.SortFunc(out, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return out, nil
}

// target is the world's deliverer.Target.
type target struct{ *world }

func (t target) Capabilities(context.Context) (string, []string, error) {
	return "v1.34.1", []string{"v1", "v1/ConfigMap", "apps/v1"}, nil
}

func (t target) Prepare(_ context.Context, env deliverer.Environment) error {
	if t.prepareErr != nil {
		return t.prepareErr
	}
	t.log("prepared namespace %s", env.Namespace)
	return nil
}

func (t target) Apply(_ context.Context, env deliverer.Environment, manifests []byte) ([]deliverer.Object, error) {
	t.log("applied %q to %s", manifests, env.Namespace)
	return t.applied, t.applyErr
}

func (t target) Prune(_ context.Context, _ deliverer.Environment, previous, current []deliverer.Object) error {
	t.log("pruned %d against %d", len(previous), len(current))
	return t.pruneErr
}

func (t target) Health(_ context.Context, objects []deliverer.Object) ([]deliverer.Object, code.Health, error) {
	if t.healthErr != nil {
		return nil, "", t.healthErr
	}
	out := slices.Clone(objects)
	for i := range out {
		out[i].Health = code.Healthy
		if h, ok := t.healths[out[i].Name]; ok {
			out[i].Health, out[i].Message = h, "waits for its pods"
		}
	}
	return out, t.health, nil
}

// archive is the world's deliverer.Archive.
type archive struct{ *world }

func (a archive) Put(_ context.Context, _, deliveryID uuid.UUID, container string, log []byte) error {
	a.log("kept %s's %s log, %d bytes", deliveryID.String()[:4], container, len(log))
	return nil
}

var settings = deliverer.Settings{Pods: true, Lease: 2 * time.Minute, RoomRetry: 30 * time.Second, Timeout: 10 * time.Minute,
	RESTURL: "http://rest", GitURL: "http://git/", LogLimit: 1 << 20, Vanished: time.Minute, HealthEvery: 15 * time.Second}

// service is a Service over w, at now, delivering into a cluster unless noCluster.
func service(w *world, s deliverer.Settings, noCluster bool) *deliverer.Service {
	var t deliverer.Target = target{w}
	if noCluster {
		t = nil
	}
	svc := deliverer.NewService(store{w}, pods{w}, t, archive{w}, s)
	svc.SetClock(func() time.Time { return now })
	return svc
}

// delivery is a queued delivery of acme/site's preview to namespace website-x.
func delivery() deliverer.Delivery {
	return deliverer.Delivery{ID: uuid.New(), OrgID: uuid.New(), RepoID: uuid.New(), EnvironmentID: uuid.New(), TargetID: uuid.New(), Number: 7,
		Target: "preview", Branch: "feature/x", SHA: strings.Repeat("a", 40), OrgSlug: "acme", RepoSlug: "site", Namespace: "website-x"}
}

// TestDispatch renders a claimed delivery in a pod of its organization's job namespace, or ends it, saying why it
// can't render, or puts it off while the quota has no room.
func TestDispatch(t *testing.T) {
	tests := []struct {
		name      string
		settings  func(*deliverer.Settings)
		noCluster bool
		world     func(*world)
		want      []string
	}{
		{name: "a queued delivery", want: []string{"claimed #7 until 09:02:00", "prepared job namespace", "started render pod", "rendering"}},
		{name: "no pods", settings: func(s *deliverer.Settings) { s.Pods = false },
			want: []string{"claimed #7 until 09:02:00", "failed: this installation runs no pods, and a delivery renders in one"}},
		{name: "no cluster of its own", noCluster: true,
			want: []string{"claimed #7 until 09:02:00", "failed: this installation delivers to no cluster of its own"}},
		{name: "no Git", settings: func(s *deliverer.Settings) { s.GitURL = "" },
			want: []string{"claimed #7 until 09:02:00", "failed: this installation serves no Git over HTTPS to check the commit out from"}},
		{name: "no room in the quota", world: func(w *world) { w.startErr = fmt.Errorf("pod: %w", runner.ErrNoRoom) },
			want: []string{"claimed #7 until 09:02:00", "prepared job namespace", "deferred until 09:00:30: waits for room in the organization's quota"}},
		{name: "a pod the cluster refuses", world: func(w *world) { w.startErr = fmt.Errorf("%w: bad image", runner.ErrRefused) },
			want: []string{"claimed #7 until 09:02:00", "prepared job namespace", "failed: the cluster refused its render pod: bad image"}},
		{name: "superseded while its pod was made", world: func(w *world) { w.superseded = true },
			want: []string{"claimed #7 until 09:02:00", "prepared job namespace", "started render pod", "removed render pod"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, s := newWorld(), settings
			if tt.settings != nil {
				tt.settings(&s)
			}
			if tt.world != nil {
				tt.world(w)
			}
			d := delivery()
			w.queued = []deliverer.Delivery{d}
			started, err := service(w, s, tt.noCluster).Dispatch(context.Background())
			if err != nil || !started {
				t.Fatalf("Dispatch() = %t, %v", started, err)
			}
			if got := trimmed(w.events); !slices.Equal(got, tt.want) {
				t.Errorf("events = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDispatchRenders starts `cobranch render` for the delivery's target, namespace and commit, for what the cluster
// serves, uploading with a token of its own that outlives the render's timeout.
func TestDispatchRenders(t *testing.T) {
	w, d := newWorld(), delivery()
	w.queued = []deliverer.Delivery{d}
	if _, err := service(w, settings, false).Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := w.started[0]
	wantArgs := []string{"--target", "preview", "--namespace", "website-x", "--branch", "feature/x", "--commit", d.SHA,
		"--repository", "site", "--organization", "acme", "--kube-version", "v1.34.1", "--api-versions", "v1,v1/ConfigMap,apps/v1"}
	if !slices.Equal(r.Args, wantArgs) {
		t.Errorf("args = %q, want %q", r.Args, wantArgs)
	}
	token := r.Env["COBRANCH_TOKEN"]
	if !strings.HasPrefix(token, "cbd_") || !slices.Equal(r.Masked, []string{token}) {
		t.Errorf("token %q masked as %q, want a delivery token, masked", token, r.Masked)
	}
	for k, v := range map[string]string{"COBRANCH_SHA": d.SHA, "COBRANCH_GIT_URL": "http://git/acme/site.git",
		"COBRANCH_UPLOAD_URL": "http://rest/v1/orgs/acme/repos/site/deliveries/7/manifests"} {
		if r.Env[k] != v {
			t.Errorf("%s = %q, want %q", k, r.Env[k], v)
		}
	}
	if got, want := w.tokens[d.ID], now.Add(25*time.Minute); !got.Equal(want) || r.Timeout != 10*time.Minute {
		t.Errorf("token expires %s with a timeout of %s, want %s and 10m", got, r.Timeout, want)
	}
}

// TestDispatchNoneWaits reports that no delivery waited.
func TestDispatchNoneWaits(t *testing.T) {
	w := newWorld()
	if started, err := service(w, settings, false).Dispatch(context.Background()); started || err != nil || len(w.events) > 0 {
		t.Errorf("Dispatch() = %t, %v, with %q, want nothing", started, err, w.events)
	}
}

// object is a ConfigMap of name, as an inventory holds it.
func object(name string) deliverer.Object {
	return deliverer.Object{Version: "v1", Kind: "ConfigMap", Namespace: "website-x", Name: name, UID: "uid-" + name, Health: code.Unknown}
}

// TestFollowApplies applies an uploaded render, or an apply a controller left, prunes and records it, and records
// how far an apply that fails got.
func TestFollowApplies(t *testing.T) {
	rendered := func(w *deliverer.Working) { w.Rendered = true }
	tests := []followCase{
		{name: "an uploaded render", working: rendered, taken: true, want: applied(`recorded succeeded, healthy: successful "Healthy"`),
			inventory: []string{"ConfigMap/site:healthy", "ConfigMap/settings:healthy"}},
		{name: "an apply a controller left", working: func(w *deliverer.Working) { w.State = code.DeliveryApplying }, taken: true,
			want: applied(`recorded succeeded, healthy: successful "Healthy"`), inventory: []string{"ConfigMap/site:healthy", "ConfigMap/settings:healthy"}},
		{name: "a render that progresses", working: rendered, taken: true,
			world: func(w *world) {
				w.health, w.healths = code.Progressing, map[string]code.Health{"settings": code.Progressing}
			},
			want:      applied(`recorded succeeded, progressing: in_progress "Progressing"`),
			inventory: []string{"ConfigMap/site:healthy", "ConfigMap/settings:progressing"}},
		{name: "a degraded render", working: rendered, taken: true,
			world:     func(w *world) { w.health, w.healths = code.Degraded, map[string]code.Health{"settings": code.Degraded} },
			want:      applied(`recorded succeeded, degraded: failed "Degraded: ConfigMap/settings: waits for its pods"`),
			inventory: []string{"ConfigMap/site:healthy", "ConfigMap/settings:degraded"}},
		{name: "health that can't be read", working: rendered, taken: true, world: func(w *world) { w.healthErr = errors.New("forbidden") },
			want: applied(`recorded succeeded, unknown: in_progress "Unknown"`), inventory: []string{"ConfigMap/site:unknown", "ConfigMap/settings:unknown"}},
		{name: "a render the cluster refuses", working: rendered, taken: true,
			world: func(w *world) {
				w.applied, w.applyErr = nil, fmt.Errorf("%w: ConfigMap/site: rendered twice", deliverer.ErrRefused)
			},
			want: applying("prepared namespace website-x", `applied "kind: ConfigMap" to website-x`,
				`recorded failed, healthy: failed "The cluster refused the render: ConfigMap/site: rendered twice"`),
			inventory: []string{"ConfigMap/site:unknown", "ConfigMap/old:unknown"}},
		{name: "an apply that fails partway", working: rendered, taken: true,
			world: func(w *world) { w.applied, w.applyErr = w.applied[:1], errors.New("the API server went away") },
			want: applying("prepared namespace website-x", `applied "kind: ConfigMap" to website-x`,
				`recorded failed, healthy: failed "Applying failed: the API server went away"`),
			inventory: []string{"ConfigMap/site:unknown", "ConfigMap/old:unknown"}},
		{name: "a prune that fails", working: rendered, taken: true, world: func(w *world) { w.pruneErr = errors.New("deleting ConfigMap/old: forbidden") },
			want:      applied(`recorded failed, healthy: failed "Applied, but deleting what the render no longer holds failed: deleting ConfigMap/old: forbidden"`),
			inventory: []string{"ConfigMap/site:unknown", "ConfigMap/settings:unknown", "ConfigMap/old:unknown"}},
		{name: "a namespace the cluster refuses", working: rendered, taken: true,
			world: func(w *world) {
				w.prepareErr = fmt.Errorf("%w: namespace website-x exists, and isn't this environment's", deliverer.ErrRefused)
			},
			want:      applying(`recorded failed, healthy: failed "Preparing namespace website-x failed: namespace website-x exists, and isn't this environment's"`),
			inventory: []string{"ConfigMap/site:unknown", "ConfigMap/old:unknown"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

// applying is what following a delivery whose apply it takes does: steps, then keeping its render pod's logs and
// removing the pod.
func applying(steps ...string) []string {
	return slices.Concat([]string{"applying #7 until 09:02:00"}, steps, []string{"kept checkout log", "kept render log", "removed render pod"})
}

// applied is what following a delivery whose render applies whole does, recording record.
func applied(record string) []string {
	return applying("prepared namespace website-x", `applied "kind: ConfigMap" to website-x`, "pruned 2 against 2", record)
}

// TestFollowEndsRenders fails a delivery whose render pod failed, vanished, timed out or ended without uploading,
// keeping its logs and removing its pod, and leaves one whose pod runs.
func TestFollowEndsRenders(t *testing.T) {
	failed := runner.Pod{Outcome: runner.Failed, Reason: "container render exited with 1"}
	ended := func(reason string) []string {
		return []string{"kept checkout log", "kept render log", "failed: " + reason, "removed render pod"}
	}
	tests := []followCase{
		{name: "a pod that ended without uploading", pod: &runner.Pod{Outcome: runner.Succeeded},
			want: ended("its render pod ended without uploading a render")},
		{name: "a pod that failed", pod: &failed, world: func(w *world) {
			w.logs["render"] = []byte("Rendering chart.\ncobranch render: root web: chart.yaml not found\n")
		}, want: ended("rendering failed: root web: chart.yaml not found")},
		{name: "a pod that failed without a log", pod: &failed, world: func(w *world) { w.logs = map[string][]byte{} },
			want: []string{"failed: rendering failed: container render exited with 1", "removed render pod"}},
		{name: "a pod that runs", pod: &runner.Pod{Outcome: runner.Pending}},
		{name: "a pod that ran past its timeout", pod: &runner.Pod{Outcome: runner.Pending},
			working: func(w *deliverer.Working) { w.StartedAt = now.Add(-16 * time.Minute) },
			want:    ended("rendering timed out after 10m0s without its pod running")},
		{name: "a pod not seen yet"},
		{name: "a pod that vanished", working: func(w *deliverer.Working) { w.StartedAt = now.Add(-2 * time.Minute) },
			want: ended("its render pod vanished")},
		{name: "no cluster any more", noCluster: true, working: func(w *deliverer.Working) { w.Rendered = true },
			want: ended("this installation delivers to no cluster of its own")},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

// followCase is a delivery that works, as Follow finds it: its render pod, whether its apply is to be taken, and
// the world it is followed in; and what following it does, and the inventory it records.
type followCase struct {
	name      string
	working   func(*deliverer.Working)
	pod       *runner.Pod
	noCluster bool
	taken     bool
	world     func(*world)
	want      []string
	inventory []string
}

// run follows the case's delivery once and checks what it did.
func (tt followCase) run(t *testing.T) {
	w, d := newWorld(), delivery()
	w.applied = []deliverer.Object{object("site"), object("settings")}
	work := deliverer.Working{ID: d.ID, OrgID: d.OrgID, RepoID: d.RepoID, Number: 7, State: code.DeliveryRendering, StartedAt: now.Add(-30 * time.Second)}
	if tt.working != nil {
		tt.working(&work)
	}
	w.working = []deliverer.Working{work}
	if tt.taken {
		w.applies[d.ID] = deliverer.Apply{ID: d.ID, OrgID: d.OrgID, RepoID: d.RepoID, EnvironmentID: d.EnvironmentID, Number: 7,
			Target: "preview", SHA: d.SHA, Namespace: "website-x", Health: code.Healthy, Manifests: []byte("kind: ConfigMap"),
			Inventory: []deliverer.Object{object("site"), object("old")}}
	}
	w.pods[d.OrgID] = map[uuid.UUID]runner.Pod{}
	if tt.pod != nil {
		w.pods[d.OrgID][d.ID] = *tt.pod
	}
	if tt.world != nil {
		tt.world(w)
	}
	if err := service(w, settings, tt.noCluster).Follow(context.Background()); err != nil {
		t.Fatalf("Follow() = %v", err)
	}
	if got := trimmed(w.events); !slices.Equal(got, tt.want) {
		t.Errorf("events = %q, want %q", got, tt.want)
	}
	if tt.inventory == nil {
		return
	}
	if got := inventory(w.recorded[0].Inventory); !slices.Equal(got, tt.inventory) {
		t.Errorf("inventory = %q, want %q", got, tt.inventory)
	}
}

// TestFollowReadsHealth reads a Progressing environment's health again every HealthEvery, and not between.
func TestFollowReadsHealth(t *testing.T) {
	w, env := newWorld(), deliverer.Environment{ID: uuid.New(), OrgID: uuid.New(), RepoID: uuid.New(), Namespace: "website-x"}
	w.progressing, w.objects[env.ID] = []deliverer.Environment{env}, []deliverer.Object{object("site")}
	clock := now
	svc := service(w, settings, false)
	svc.SetClock(func() time.Time { return clock })
	ctx := context.Background()
	for _, after := range []time.Duration{0, 10 * time.Second, 6 * time.Second} {
		clock = clock.Add(after)
		if err := svc.Follow(ctx); err != nil {
			t.Fatal(err)
		}
	}
	read := "environment " + env.ID.String()[:4] + ` healthy: successful "Healthy"`
	if want := []string{read, read}; !slices.Equal(w.events, want) {
		t.Errorf("events = %q, want %q", w.events, want)
	}
	if err := service(w, settings, true).Follow(ctx); err != nil || len(w.events) != 2 {
		t.Errorf("Follow() without a cluster = %v, with %q, want nothing read", err, w.events)
	}
}

// TestSweep removes the render pods of deliveries that ended, keeping their logs, and of those that are gone, and
// leaves those of deliveries that are queued or render.
func TestSweep(t *testing.T) {
	w, org := newWorld(), uuid.New()
	queued, rendering, ended, gone := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	w.pods[org] = map[uuid.UUID]runner.Pod{queued: {}, rendering: {}, ended: {Outcome: runner.Succeeded}, gone: {Outcome: runner.Failed}}
	w.standing = map[uuid.UUID]deliverer.Standing{queued: {State: code.DeliveryQueued}, rendering: {State: code.DeliveryRendering},
		ended: {State: code.DeliveryCancelled}}
	if err := service(w, settings, false).Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, id := range sorted(queued, rendering, ended, gone) {
		switch id {
		case ended:
			want = append(want, "kept "+id.String()[:4]+"'s checkout log, 13 bytes", "kept "+id.String()[:4]+"'s render log, 17 bytes",
				"removed render pod "+id.String()[:4])
		case gone:
			want = append(want, "removed render pod "+id.String()[:4])
		}
	}
	if want = append([]string{"dropped dead tokens"}, want...); !slices.Equal(w.events, want) {
		t.Errorf("events = %q, want %q", w.events, want)
	}
}

// TestNoPods sweeps nothing and renders nothing: deliveries fail, saying why.
func TestNoPods(t *testing.T) {
	w := newWorld()
	svc := deliverer.NewService(store{w}, deliverer.NoPods{}, nil, archive{w}, deliverer.Settings{})
	ctx := context.Background()
	if err := svc.Sweep(ctx); err != nil {
		t.Errorf("Sweep() = %v", err)
	}
	if err := svc.Follow(ctx); err != nil {
		t.Errorf("Follow() = %v", err)
	}
	if want := []string{"dropped dead tokens"}; !slices.Equal(w.events, want) {
		t.Errorf("events = %q, want %q", w.events, want)
	}
}

// trimmed is events without the IDs some end with, which differ from run to run.
func trimmed(events []string) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		switch {
		case strings.HasPrefix(e, "prepared job namespace "), strings.HasPrefix(e, "removed render pod "):
			e = e[:strings.LastIndex(e, " ")]
		case strings.HasPrefix(e, "kept "):
			e = "kept " + strings.SplitN(strings.TrimPrefix(e, "kept "), "'s ", 2)[1]
			e = e[:strings.Index(e, ",")]
		}
		out = append(out, e)
	}
	return out
}

// inventory is objects as kind/name:health.
func inventory(objects []deliverer.Object) []string {
	out := make([]string, 0, len(objects))
	for _, o := range objects {
		out = append(out, o.String()+":"+string(o.Health))
	}
	return out
}

// sorted is ids in the order Sweep takes them.
func sorted(ids ...uuid.UUID) []uuid.UUID {
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return ids
}
