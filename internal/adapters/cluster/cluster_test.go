package cluster_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"cobranch.dev/internal/adapters/cluster"
)

var (
	configMaps = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	namespaces = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	crds       = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
)

// assetsEnv names the variable that holds the directory with the etcd and kube-apiserver binaries the tests start a
// throwaway API server from. CI sets it; without it, the tests that need one are skipped, except in CI, where they
// fail.
const assetsEnv = "COBRANCH_TEST_KUBE_ASSETS"

// server is the throwaway API server's client configuration, nil without assetsEnv.
var server *rest.Config

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

// run starts the throwaway API server when assetsEnv names its binaries, and runs the tests.
func run(m *testing.M) int {
	assets := os.Getenv(assetsEnv)
	if assets == "" {
		return m.Run()
	}
	env := &envtest.Environment{BinaryAssetsDirectory: assets}
	cfg, err := env.Start()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "starting the API server:", err) // stderr is gone; nothing to tell
		return 1
	}
	defer func() {
		if err := env.Stop(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "stopping the API server:", err) // the run is over; nothing to tell
		}
	}()
	server = cfg
	return m.Run()
}

// fixture is the throwaway API server, with a namespace of the test's own and an environment there.
type fixture struct {
	cluster *cluster.Cluster
	dyn     dynamic.Interface
	env     cluster.Environment
}

// newFixture is a fixture, or skips the test without an API server, except in CI.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	if server == nil {
		if os.Getenv("CI") != "" {
			t.Fatal(assetsEnv + " is not set, and CI must run the tests that need an API server")
		}
		t.Skip(assetsEnv + " is not set")
	}
	c, err := cluster.New(server)
	if err != nil {
		t.Fatal(err)
	}
	dyn, err := dynamic.NewForConfig(server)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{cluster: c, dyn: dyn, env: cluster.Environment{ID: uuid.NewString(), Namespace: "delivery-" + uuid.NewString()[:8]}}
	ns := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": f.env.Namespace}}}
	if _, err := dyn.Resource(namespaces).Create(context.Background(), ns, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	return f
}

// configMap is ConfigMap name in namespace, holding data, with labels.
func configMap(namespace, name string, data map[string]any, labels map[string]string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": name}, "data": data}}
	u.SetNamespace(namespace)
	u.SetLabels(labels)
	return u
}

// render is a render's stream of objects, as the controller downloads it.
func render(t *testing.T, docs ...string) []*unstructured.Unstructured {
	t.Helper()
	objs, err := cluster.Decode([]byte("---\n" + strings.Join(docs, "\n---\n")))
	if err != nil {
		t.Fatal(err)
	}
	return objs
}

// names is refs' kind/name, in order.
func names(refs []cluster.Ref) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Kind+"/"+r.Name)
	}
	return out
}

// configMapsIn is the names of the ConfigMaps in f's namespace.
func (f *fixture) configMapsIn(t *testing.T) []string {
	t.Helper()
	list, err := f.dyn.Resource(configMaps).Namespace(f.env.Namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, u := range list.Items {
		if u.GetName() != "kube-root-ca.crt" {
			out = append(out, u.GetName())
		}
	}
	slices.Sort(out)
	return out
}

// takeMode sets ConfigMap name's data.mode as another field manager, forcing it.
func (f *fixture) takeMode(t *testing.T, name string) {
	t.Helper()
	theirs := configMap(f.env.Namespace, name, map[string]any{"mode": "theirs"}, nil)
	opts := metav1.ApplyOptions{FieldManager: "someone", Force: true}
	if _, err := f.dyn.Resource(configMaps).Namespace(f.env.Namespace).Apply(context.Background(), name, theirs, opts); err != nil {
		t.Fatal(err)
	}
}

// mode is ConfigMap name's data.mode.
func (f *fixture) mode(t *testing.T, name string) string {
	t.Helper()
	u, err := f.dyn.Resource(configMaps).Namespace(f.env.Namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	mode, _, _ := unstructured.NestedString(u.Object, "data", "mode") // missing is "", which the test reports
	return mode
}

func TestApplyPruneAndHealth(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	first, err := f.cluster.Apply(ctx, f.env, render(t,
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\ndata:\n  mode: first",
		"apiVersion: v1\nkind: Secret\nmetadata:\n  name: b\nstringData:\n  token: s",
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n  labels:\n    app: shop"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(first), []string{"Secret/b", "ConfigMap/a", "ConfigMap/c"}; !slices.Equal(got, want) {
		t.Fatalf("Apply() = %v, want %v", got, want)
	}
	c, err := f.dyn.Resource(configMaps).Namespace(f.env.Namespace).Get(ctx, "c", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.GetLabels(); got[cluster.EnvironmentLabel] != f.env.ID || got["app"] != "shop" || string(c.GetUID()) != first[2].UID {
		t.Errorf("ConfigMap/c = labels %v, UID %s; want the environment's label and the inventory's UID", got, c.GetUID())
	}
	objects, health, err := f.cluster.Health(ctx, first)
	if err != nil || health != cluster.Healthy || len(objects) != 3 {
		t.Fatalf("Health() = %v, %s, %v, want 3 Healthy", objects, health, err)
	}

	f.takeMode(t, "a") // someone else takes a field, and the next apply takes it back
	second, err := f.cluster.Apply(ctx, f.env, render(t,
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\ndata:\n  mode: second",
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: d"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := f.mode(t, "a"); mode != "second" {
		t.Errorf("ConfigMap/a's mode = %q, want second", mode)
	}

	// An object the inventory names but someone else now owns stays.
	if _, err := f.dyn.Resource(configMaps).Namespace(f.env.Namespace).Create(ctx, configMap(f.env.Namespace, "x", nil, nil), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	stale := append(slices.Clone(first), cluster.Ref{Version: "v1", Kind: "ConfigMap", Namespace: f.env.Namespace, Name: "x", UID: "gone"})
	if err := f.cluster.Prune(ctx, f.env, stale, second); err != nil {
		t.Fatal(err)
	}
	if got, want := f.configMapsIn(t), []string{"a", "d", "x"}; !slices.Equal(got, want) {
		t.Errorf("after the prune, ConfigMaps = %v, want %v", got, want)
	}

	// A teardown deletes the rest, and is safe to repeat.
	for range 2 {
		if err := f.cluster.Prune(ctx, f.env, second, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := f.configMapsIn(t), []string{"x"}; !slices.Equal(got, want) {
		t.Errorf("after the teardown, ConfigMaps = %v, want %v", got, want)
	}
	if _, health, err := f.cluster.Health(ctx, second); err != nil || health != cluster.Missing {
		t.Errorf("Health() after the teardown = %s, %v, want Missing", health, err)
	}
}

func TestApplyRefuses(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	other := uuid.NewString()
	for _, u := range []*unstructured.Unstructured{
		configMap(f.env.Namespace, "theirs", nil, nil),
		configMap(f.env.Namespace, "another", nil, map[string]string{cluster.EnvironmentLabel: other}),
	} {
		if _, err := f.dyn.Resource(configMaps).Namespace(f.env.Namespace).Create(ctx, u, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name string
		own  bool
		docs []string
		want string
	}{
		{name: "an object Cobranch didn't create", docs: []string{"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: fresh",
			"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: theirs"},
			want: "refused: ConfigMap/theirs in namespace " + f.env.Namespace + ": it already exists, and Cobranch didn't create it"},
		{name: "another environment's object", docs: []string{"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: another"},
			want: "refused: ConfigMap/another in namespace " + f.env.Namespace + ": it belongs to another environment, " + other},
		{name: "a kind the cluster doesn't serve", docs: []string{"apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w"},
			want: "refused: Widget/w: the cluster serves no example.com/v1, Kind=Widget"},
		{name: "a Namespace", docs: []string{"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: " + f.env.Namespace},
			want: "refused: Namespace/" + f.env.Namespace + ": the environment's namespace is the environment's own; a render may not hold a Namespace"},
		{name: "a cluster-scoped object in the installation's cluster", own: true,
			docs: []string{"apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: reader-" + f.env.Namespace},
			want: "refused: ClusterRole/reader-" + f.env.Namespace + ": it is cluster-scoped, and this installation's cluster takes namespaced objects only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := f.env
			env.Own = tt.own
			refs, err := f.cluster.Apply(ctx, env, render(t, tt.docs...))
			if !errors.Is(err, cluster.ErrRefused) || err.Error() != tt.want || refs != nil {
				t.Fatalf("Apply() = %v, %v, want ErrRefused %q", refs, err, tt.want)
			}
		})
	}
	if got, want := f.configMapsIn(t), []string{"another", "theirs"}; !slices.Equal(got, want) {
		t.Errorf("after the refusals, ConfigMaps = %v, want %v: a refused render applies nothing", got, want)
	}
}

func TestApplyCRDs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	group := strings.TrimPrefix(f.env.Namespace, "delivery-") + ".example.com"
	crd := fmt.Sprintf(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.%[1]s
spec:
  group: %[1]s
  scope: Namespaced
  names: {kind: Widget, plural: widgets, singular: widget, listKind: WidgetList}
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-preserve-unknown-fields: true`, group)
	t.Cleanup(func() {
		_ = f.dyn.Resource(crds).Delete(context.Background(), "widgets."+group, metav1.DeleteOptions{}) // the cluster is the test's to throw away
	})
	f.cluster.Establish = 30 * time.Second
	refs, err := f.cluster.Apply(ctx, f.env, render(t, "apiVersion: "+group+"/v1\nkind: Widget\nmetadata:\n  name: w\nspec:\n  size: 3", crd))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(refs), []string{"CustomResourceDefinition/widgets." + group, "Widget/w"}; !slices.Equal(got, want) {
		t.Fatalf("Apply() = %v, want %v", got, want)
	}
	if err := f.cluster.Prune(ctx, f.env, refs, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.dyn.Resource(crds).Get(ctx, "widgets."+group, metav1.GetOptions{}); err != nil {
		t.Errorf("the teardown deleted the CRD: %v", err)
	}
	widgets := schema.GroupVersionResource{Group: group, Version: "v1", Resource: "widgets"}
	if _, err := f.dyn.Resource(widgets).Namespace(f.env.Namespace).Get(ctx, "w", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("after the teardown, Widget/w = %v, want gone", err)
	}
}

func TestCapabilities(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	version, apis, err := f.cluster.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(version, "v1.") {
		t.Errorf("version = %q, want v1.x", version)
	}
	for _, want := range []string{"v1", "v1/ConfigMap", "apps/v1", "apps/v1/Deployment"} {
		if !slices.Contains(apis, want) {
			t.Errorf("API versions lack %s", want)
		}
	}
	if slices.ContainsFunc(apis, func(s string) bool { return strings.HasSuffix(s, "/status") }) {
		t.Error("API versions hold subresources")
	}
}
