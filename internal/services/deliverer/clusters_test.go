package deliverer_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/deliverer"
	"cobranch.dev/internal/services/secrets"
)

// TestDispatchOtherCluster renders a delivery to another cluster once its target reads the kubeconfig in the cluster's
// secret and the controller opens it, for what that cluster serves; and fails it, saying why, when the target may not
// read the secret, the secret is gone, the kubeconfig is refused or the cluster doesn't answer.
func TestDispatchOtherCluster(t *testing.T) {
	secret := uuid.MustParse("0193a8c2-5c1e-7f20-9d4b-3e1f6a2b7c90")
	claimed := "claimed #7 until 09:02:00"
	tests := []struct {
		name  string
		world func(*world)
		want  []string
	}{
		{name: "a kubeconfig the target reads", want: []string{claimed, `opened "apiVersion: v1"`, "prepared job namespace", "started render pod", "rendering"}},
		{name: "a secret the target may not read", world: func(w *world) { w.secretErr = &secrets.RefusedError{ID: secret} },
			want: []string{claimed, "failed: target preview may not read secret " + secret.String() + ", its cluster: grant it Secret reader on the secret"}},
		{name: "a secret that is gone", world: func(w *world) { w.secretErr = &secrets.RefusedError{ID: secret, Missing: true} },
			want: []string{claimed, "failed: secret " + secret.String() + ", target preview's cluster, isn't there"}},
		{name: "no secrets key", world: func(w *world) { w.secretErr = secrets.ErrNoSecretsKey },
			want: []string{claimed, "failed: this installation has no secrets key to open its secrets with"}},
		{name: "a kubeconfig the controller refuses", world: func(w *world) {
			w.openErr = fmt.Errorf("%w: user ci: exec plugins are refused", deliverer.ErrRefused)
		}, want: []string{claimed, "failed: the kubeconfig in secret " + secret.String() + ": user ci: exec plugins are refused"}},
		{name: "a cluster that doesn't answer", world: func(w *world) { w.capErr = errors.New("dial tcp 10.0.0.1:443: i/o timeout") },
			want: []string{claimed, `opened "apiVersion: v1"`, "failed: reaching the cluster failed: dial tcp 10.0.0.1:443: i/o timeout"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, d := newWorld(), delivery()
			d.Cluster = &secret
			w.kubeconfigs[secret] = []byte("apiVersion: v1")
			if tt.world != nil {
				tt.world(w)
			}
			w.queued = []deliverer.Delivery{d}
			// Without a cluster of its own, the installation still delivers to others.
			if _, err := service(w, settings, true).Dispatch(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := trimmed(w.events); !slices.Equal(got, tt.want) {
				t.Errorf("events = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDispatchResync applies an environment's last render again, as it applies a commit's, without a render pod or an
// annotation, and fails a resync that finds no render.
func TestDispatchResync(t *testing.T) {
	tests := []struct {
		name     string
		rendered bool
		want     []string
	}{
		{name: "a render to apply again", rendered: true, want: []string{"claimed #7 until 09:02:00", "applying #7 until 09:02:00",
			"prepared namespace website-x", `applied "kind: ConfigMap" to website-x`, "pruned 2 against 2",
			`recorded succeeded, healthy: successful "Healthy"`}},
		{name: "no render", want: []string{"claimed #7 until 09:02:00", "applying #7 until 09:02:00",
			`recorded failed, healthy: failed "No delivery of a commit to this environment succeeded, so there is no render to apply again"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, d := newWorld(), delivery()
			d.Kind = code.Resync
			w.queued = []deliverer.Delivery{d}
			w.applied = []deliverer.Object{object("site"), object("settings")}
			w.applies[d.ID] = deliverer.Apply{ID: d.ID, OrgID: d.OrgID, RepoID: d.RepoID, EnvironmentID: d.EnvironmentID, Number: 7,
				Kind: code.Resync, Target: "preview", SHA: d.SHA, Namespace: "website-x", Health: code.Healthy,
				Manifests: []byte("kind: ConfigMap"), Rendered: tt.rendered, Inventory: []deliverer.Object{object("site"), object("settings")}}
			if _, err := service(w, settings, false).Dispatch(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(w.events, tt.want) {
				t.Errorf("events = %q, want %q", w.events, tt.want)
			}
			if len(w.recorded) != 1 || w.recorded[0].Apply.Kind != code.Resync {
				t.Errorf("recorded %v, want the resync's outcome", w.recorded)
			}
		})
	}
}

// TestSchedule queues the environments' resyncs and teardowns every Resync.
func TestSchedule(t *testing.T) {
	w := newWorld()
	if err := service(w, settings, false).Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"scheduled every 10m0s"}; !slices.Equal(w.events, want) {
		t.Errorf("events = %q, want %q", w.events, want)
	}
}
