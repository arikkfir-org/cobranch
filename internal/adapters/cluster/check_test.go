package cluster

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// obj is a rendered object of apiVersion and kind, named name in namespace.
func obj(apiVersion, kind, namespace, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": apiVersion, "kind": kind, "metadata": map[string]any{"name": name}}}
	if namespace != "" {
		u.SetNamespace(namespace)
	}
	return u
}

var env = Environment{ID: "0193a8c2-71d0-7a44-8e5f-0c2d9b1e4f63", Namespace: "shop-preview"}

var checkTests = []struct {
	name       string
	own        bool
	objs       []object
	want       []string
	namespaces []string
}{
	{name: "namespaced objects take the environment's namespace",
		objs:       []object{{u: obj("v1", "ConfigMap", "", "a"), namespaced: true}, {u: obj("v1", "Secret", "shop-preview", "b"), namespaced: true}},
		namespaces: []string{"shop-preview", "shop-preview"}},
	{name: "a cluster-scoped object in another cluster", objs: []object{{u: obj("rbac.authorization.k8s.io/v1", "ClusterRole", "x", "reader")}},
		namespaces: []string{""}},
	{name: "a cluster-scoped object in the installation's cluster", own: true, objs: []object{{u: obj("rbac.authorization.k8s.io/v1", "ClusterRole", "", "reader")}},
		want: []string{"ClusterRole/reader: it is cluster-scoped, and this installation's cluster takes namespaced objects only"}},
	{name: "a Namespace", objs: []object{{u: obj("v1", "Namespace", "", "prod")}},
		want: []string{"Namespace/prod: the environment's namespace is the environment's own; a render may not hold a Namespace"}},
	{name: "another namespace", objs: []object{{u: obj("v1", "ConfigMap", "kube-system", "a"), namespaced: true}},
		want: []string{"ConfigMap/a in namespace kube-system: it names a namespace other than the environment's, shop-preview"}},
	{name: "an object twice", objs: []object{{u: obj("v1", "ConfigMap", "", "a"), namespaced: true}, {u: obj("v1", "ConfigMap", "shop-preview", "a"), namespaced: true}},
		want: []string{"ConfigMap/a in namespace shop-preview: rendered twice"}},
	{name: "the same name in two versions", objs: []object{{u: obj("apps/v1", "Deployment", "", "a"), namespaced: true}, {u: obj("apps/v1beta1", "Deployment", "", "a"), namespaced: true}},
		want: []string{"Deployment/a in namespace shop-preview: rendered twice"}},
	{name: "a name in two kinds", objs: []object{{u: obj("v1", "ConfigMap", "", "a"), namespaced: true}, {u: obj("v1", "Secret", "", "a"), namespaced: true}},
		namespaces: []string{"shop-preview", "shop-preview"}},
	{name: "every refusal named", own: true, objs: []object{{u: obj("v1", "Namespace", "", "a")}, {u: obj("v1", "ConfigMap", "other", "b"), namespaced: true}},
		want: []string{"Namespace/a: the environment's namespace is the environment's own; a render may not hold a Namespace",
			"ConfigMap/b in namespace other: it names a namespace other than the environment's, shop-preview"}},
}

func TestCheck(t *testing.T) {
	for _, tt := range checkTests {
		t.Run(tt.name, func(t *testing.T) {
			e := env
			e.Own = tt.own
			accepted, got := check(e, tt.objs)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("check() = %q, want %q", got, tt.want)
			}
			if tt.want != nil {
				if len(accepted)+len(got) != len(tt.objs) {
					t.Errorf("check() accepted %d and refused %d of %d", len(accepted), len(got), len(tt.objs))
				}
				return
			}
			for i, o := range accepted {
				if o.u.GetNamespace() != tt.namespaces[i] || o.u.GetLabels()[EnvironmentLabel] != env.ID {
					t.Errorf("object %d = namespace %q, labels %v", i, o.u.GetNamespace(), o.u.GetLabels())
				}
			}
		})
	}
}

func TestCheckKeepsLabels(t *testing.T) {
	u := obj("v1", "ConfigMap", "", "a")
	u.SetLabels(map[string]string{"app": "shop", EnvironmentLabel: "someone-else"})
	if _, got := check(env, []object{{u: u, namespaced: true}}); got != nil {
		t.Fatal(got)
	}
	if want := map[string]string{"app": "shop", EnvironmentLabel: env.ID}; !reflect.DeepEqual(u.GetLabels(), want) {
		t.Errorf("labels = %v, want %v", u.GetLabels(), want)
	}
}

func TestOrder(t *testing.T) {
	objs := []object{
		{u: obj("apps/v1", "Deployment", "", "api")},
		{u: obj("example.com/v1", "Widget", "", "w")},
		{u: obj("v1", "Service", "", "api")},
		{u: obj("apiextensions.k8s.io/v1", "CustomResourceDefinition", "", "widgets.example.com")},
		{u: obj("example.com/v1", "Gadget", "", "g")},
		{u: obj("v1", "ConfigMap", "", "b")},
		{u: obj("v1", "ServiceAccount", "", "api")},
		{u: obj("v1", "ConfigMap", "", "a")},
	}
	order(objs)
	var got []string
	for _, o := range objs {
		got = append(got, o.u.GetKind()+"/"+o.u.GetName())
	}
	want := []string{"CustomResourceDefinition/widgets.example.com", "ServiceAccount/api", "ConfigMap/b", "ConfigMap/a", "Service/api",
		"Deployment/api", "Gadget/g", "Widget/w"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order() = %v, want %v", got, want)
	}
}

func TestCRDKinds(t *testing.T) {
	crd := obj("apiextensions.k8s.io/v1", "CustomResourceDefinition", "", "widgets.example.com")
	crd.Object["spec"] = map[string]any{"group": "example.com", "scope": "Namespaced", "names": map[string]any{"kind": "Widget", "plural": "widgets"},
		"versions": []any{map[string]any{"name": "v1"}, map[string]any{"name": "v2"}, "junk"}}
	cluster := obj("apiextensions.k8s.io/v1", "CustomResourceDefinition", "", "gizmos.example.com")
	cluster.Object["spec"] = map[string]any{"group": "example.com", "scope": "Cluster", "names": map[string]any{"kind": "Gizmo", "plural": "gizmos"},
		"versions": []any{map[string]any{"name": "v1"}}}
	got := crdKinds([]*unstructured.Unstructured{crd, obj("v1", "ConfigMap", "", "a"), cluster})
	want := map[schema.GroupVersionKind]object{
		{Group: "example.com", Version: "v1", Kind: "Widget"}: {resource: schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}, namespaced: true, pending: true},
		{Group: "example.com", Version: "v2", Kind: "Widget"}: {resource: schema.GroupVersionResource{Group: "example.com", Version: "v2", Resource: "widgets"}, namespaced: true, pending: true},
		{Group: "example.com", Version: "v1", Kind: "Gizmo"}:  {resource: schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "gizmos"}, pending: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("crdKinds() = %v, want %v", got, want)
	}
}
