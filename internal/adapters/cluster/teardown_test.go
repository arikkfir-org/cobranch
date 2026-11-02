package cluster_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"cobranch.dev/internal/adapters/cluster"
)

func TestTeardown(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	refs, err := f.cluster.Apply(ctx, f.env, render(t,
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a",
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: b",
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n  finalizers: [example.com/hold]"))
	if err != nil {
		t.Fatal(err)
	}
	f.recreate(t, "b") // someone else's now, though it carries the environment's label

	// c's finalizer holds it, and b isn't the environment's any more: c remains, and b stays without remaining.
	remaining, err := f.cluster.Teardown(ctx, f.env, refs)
	if err != nil || !slices.Equal(names(remaining), []string{"ConfigMap/c"}) {
		t.Fatalf("Teardown() = %v, %v, want ConfigMap/c, which its finalizer holds", names(remaining), err)
	}
	if got, want := f.configMapsIn(t), []string{"b", "c"}; !slices.Equal(got, want) {
		t.Errorf("after the teardown, ConfigMaps = %v, want %v", got, want)
	}
	if _, err := asStranger(t).Teardown(ctx, f.env, refs); err == nil {
		t.Error("Teardown() as a stranger = nil, want an error: it may read nothing")
	}

	patch := []byte(`{"metadata":{"finalizers":null}}`)
	if _, err := f.dyn.Resource(configMaps).Namespace(f.env.Namespace).Patch(ctx, "c", types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	if remaining, err := f.cluster.Teardown(ctx, f.env, refs); err != nil || len(remaining) != 0 {
		t.Errorf("Teardown() once the finalizer ran = %v, %v, want none", names(remaining), err)
	}
	if got, want := f.configMapsIn(t), []string{"b"}; !slices.Equal(got, want) {
		t.Errorf("after the second teardown, ConfigMaps = %v, want %v", got, want)
	}
}

func TestRelease(t *testing.T) {
	t.Parallel()
	newFixture(t) // skips without an API server
	ctx := context.Background()
	client, err := kubernetes.NewForConfig(server)
	if err != nil {
		t.Fatal(err)
	}
	namespaces := client.CoreV1().Namespaces()
	id := uuid.NewString()
	ours, theirs := "release-"+uuid.NewString()[:8], "release-"+uuid.NewString()[:8]
	for name, labels := range map[string]map[string]string{ours: {cluster.EnvironmentLabel: id}, theirs: nil} {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
		if _, err := namespaces.Create(ctx, ns, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	// The test's API server runs no namespace controller, so a deleted namespace stays as it is deleting.
	for range 2 {
		if gone, err := cluster.Release(ctx, namespaces, ours, id); gone || err != nil {
			t.Errorf("Release(the environment's) = %t, %v, want it deleting", gone, err)
		}
	}
	if ns, err := namespaces.Get(ctx, ours, metav1.GetOptions{}); err != nil || ns.DeletionTimestamp == nil {
		t.Errorf("the environment's namespace = %+v, %v, want it deleting", ns, err)
	}
	for _, name := range []string{theirs, "missing"} {
		if gone, err := cluster.Release(ctx, namespaces, name, id); !gone || err != nil {
			t.Errorf("Release(%s) = %t, %v, want it counted gone", name, gone, err)
		}
	}
	if ns, err := namespaces.Get(ctx, theirs, metav1.GetOptions{}); err != nil || ns.DeletionTimestamp != nil {
		t.Errorf("another's namespace = %+v, %v, want it left alone", ns, err)
	}
}
