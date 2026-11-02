package deliverer_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/deliverer"
)

// TestTeardown deletes what an environment holds, then its namespace, and ends Succeeded once both are gone; it looks
// again while something remains, and fails once the teardown timeout passed, naming what remains.
func TestTeardown(t *testing.T) {
	holding := "holding teardown #7 until 09:02:00"
	deleted := "deleted 2 from website-x"
	tests := []struct {
		name      string
		noCluster bool
		started   time.Duration
		world     func(*world)
		want      []string
		wantErr   bool
	}{
		{name: "all gone", want: []string{deleted, "released namespace website-x: true", `teardown succeeded "", holding []`}},
		{name: "objects that remain", world: func(w *world) { w.remaining = []deliverer.Object{object("site")} },
			want: []string{deleted, "looking again at 09:00:15"}},
		{name: "objects that remain past the timeout", started: 11 * time.Minute,
			world: func(w *world) { w.remaining = []deliverer.Object{object("site"), object("old")} },
			want: []string{deleted, `teardown failed "2 objects remain after 10m0s: ConfigMap/site, ConfigMap/old", ` +
				`holding ["ConfigMap/site:unknown" "ConfigMap/old:unknown"]`}},
		{name: "a namespace that is still deleting", world: func(w *world) { w.released = false },
			want: []string{deleted, "released namespace website-x: false", "looking again at 09:00:15"}},
		{name: "a namespace still there past the timeout", started: 11 * time.Minute, world: func(w *world) { w.released = false },
			want: []string{deleted, "released namespace website-x: false",
				`teardown failed "namespace website-x is still there after 10m0s", holding []`}},
		{name: "a delete that fails", wantErr: true, world: func(w *world) {
			w.remaining, w.teardownErr = []deliverer.Object{object("site")}, errors.New("deleting ConfigMap/site: forbidden")
		}, want: []string{deleted, "looking again at 09:00:15"}},
		{name: "a delete that fails past the timeout", started: 11 * time.Minute, world: func(w *world) {
			w.remaining, w.teardownErr = []deliverer.Object{object("site")}, errors.New("forbidden")
		}, want: []string{deleted, `teardown failed "1 object remains after 10m0s: ConfigMap/site; the last attempt failed: ` +
			`deleting environment ` + "ENV" + `'s objects: forbidden", holding ["ConfigMap/site:unknown"]`}},
		{name: "no cluster, and nothing applied there", noCluster: true, world: func(w *world) { w.inventory = nil },
			want: []string{`teardown succeeded "", holding []`}},
		{name: "no cluster, with what it holds", noCluster: true,
			want: []string{`teardown failed "this installation delivers to no cluster of its own", ` +
				`holding ["ConfigMap/site:unknown" "ConfigMap/old:unknown"]`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, env := teardownWorld(tt.world, tt.started)
			_, err := service(w, settings, tt.noCluster).Dispatch(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("Dispatch() = %v, want an error: %t", err, tt.wantErr)
			}
			want := slices.Concat([]string{"claimed #7 until 09:02:00", "deleting", holding}, tt.want)
			for i := range want {
				want[i] = strings.ReplaceAll(want[i], "ENV", env.ID.String())
			}
			if !slices.Equal(w.events, want) {
				t.Errorf("events = %q, want %q", w.events, want)
			}
		})
	}
}

// teardownWorld is a world whose one queued delivery is teardown #7 of a preview, holding site and old as change
// leaves them, started started ago.
func teardownWorld(change func(*world), started time.Duration) (*world, deliverer.Environment) {
	w, d := newWorld(), delivery()
	d.Kind, d.SHA = code.Teardown, ""
	w.inventory = []deliverer.Object{object("site"), object("old")}
	if change != nil {
		change(w)
	}
	env := deliverer.Environment{ID: d.EnvironmentID, OrgID: d.OrgID, RepoID: d.RepoID, TargetID: d.TargetID, Target: "preview",
		Namespace: "website-x"}
	w.teardowns[d.ID] = deliverer.Teardown{ID: d.ID, OrgID: d.OrgID, RepoID: d.RepoID, Number: 7, Environment: env,
		Inventory: w.inventory, StartedAt: now.Add(-started)}
	w.queued = []deliverer.Delivery{d}
	return w, env
}

// TestFollowTeardown follows a teardown that deletes, without a render pod to look at, and leaves one another
// controller holds.
func TestFollowTeardown(t *testing.T) {
	w, d := newWorld(), delivery()
	held := uuid.New()
	env := deliverer.Environment{ID: d.EnvironmentID, OrgID: d.OrgID, RepoID: d.RepoID, Namespace: "website-x"}
	w.teardowns[d.ID] = deliverer.Teardown{ID: d.ID, Number: 7, Environment: env, StartedAt: now}
	w.working = []deliverer.Working{
		{ID: d.ID, OrgID: d.OrgID, Number: 7, Kind: code.Teardown, State: code.DeliveryDeleting},
		{ID: held, OrgID: d.OrgID, Number: 8, Kind: code.Teardown, State: code.DeliveryDeleting},
	}
	if err := service(w, settings, false).Follow(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"holding teardown #7 until 09:02:00", "deleted 0 from website-x", "released namespace website-x: true",
		`teardown succeeded "", holding []`}
	if !slices.Equal(w.events, want) {
		t.Errorf("events = %q, want %q", w.events, want)
	}
}
