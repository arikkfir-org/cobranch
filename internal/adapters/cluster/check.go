package cluster

import (
	"context"
	"fmt"
	"slices"
	"strings"

	releaseutil "helm.sh/helm/v4/pkg/release/v1/util"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// object is one rendered object on its way to the cluster, with its kind's resource and scope, from the cluster's
// discovery or from a CRD of the same render, which the cluster serves only once that CRD is applied.
type object struct {
	u          *unstructured.Unstructured
	resource   schema.GroupVersionResource
	namespaced bool
	// pending is a kind only a CRD of the render defines: nothing of it can exist yet.
	pending bool
}

// String names the object for messages.
func (o object) String() string {
	return named(o.u.GetKind(), o.u.GetName(), o.u.GetNamespace())
}

// isCRD reports whether gk is CustomResourceDefinition's.
func isCRD(gk schema.GroupKind) bool {
	return gk == schema.GroupKind{Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"}
}

// crdKinds are the kinds the render's CRDs define, by group, version and kind, with their resources and scopes.
func crdKinds(rendered []*unstructured.Unstructured) map[schema.GroupVersionKind]object {
	out := map[schema.GroupVersionKind]object{}
	for _, u := range rendered {
		if !isCRD(u.GroupVersionKind().GroupKind()) {
			continue
		}
		group, kind, plural := text(u.Object, "spec", "group"), text(u.Object, "spec", "names", "kind"), text(u.Object, "spec", "names", "plural")
		namespaced := text(u.Object, "spec", "scope") == "Namespaced"
		versions, _, _ := unstructured.NestedSlice(u.Object, "spec", "versions") // none defines no kind
		for _, v := range versions {
			version, _ := v.(map[string]any) // a version that isn't a mapping names nothing
			name := text(version, "name")
			if name == "" {
				continue
			}
			out[schema.GroupVersionKind{Group: group, Version: name, Kind: kind}] = object{
				resource: schema.GroupVersionResource{Group: group, Version: name, Resource: plural}, namespaced: namespaced, pending: true}
		}
	}
	return out
}

// text is the string at fields under o, or "" where there is none.
func text(o map[string]any, fields ...string) string {
	s, _, _ := unstructured.NestedString(o, fields...) // missing, or not a string, is ""
	return s
}

// resolve finds each rendered object's resource and scope, refusing one whose kind neither the cluster nor a CRD of
// the render defines.
func (c *Cluster) resolve(rendered []*unstructured.Unstructured) ([]object, []string, error) {
	defined := crdKinds(rendered)
	objs := make([]object, 0, len(rendered))
	var refusals []string
	for _, u := range rendered {
		gvk := u.GroupVersionKind()
		m, err := c.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		switch {
		case err == nil:
			objs = append(objs, object{u: u, resource: m.Resource, namespaced: m.Scope.Name() == meta.RESTScopeNameNamespace})
		case meta.IsNoMatchError(err):
			o, ok := defined[gvk]
			if !ok {
				refusals = append(refusals, fmt.Sprintf("%s: the cluster serves no %s", object{u: u}, gvk.GroupVersion().WithKind(gvk.Kind)))
				continue
			}
			o.u = u
			objs = append(objs, o)
		default:
			return nil, nil, fmt.Errorf("looking up %s's kind: %w", object{u: u}, err)
		}
	}
	return objs, refusals, nil
}

// check refuses what env can't take, naming each object, and makes the rest env's, which it returns: a namespaced
// object without a namespace takes env's, and every object gets env's label.
func check(env Environment, objs []object) ([]object, []string) {
	var refusals []string
	accepted := make([]object, 0, len(objs))
	seen := map[string]bool{}
	for _, o := range objs {
		if reason := refused(env, o); reason != "" {
			refusals = append(refusals, o.String()+": "+reason)
			continue
		}
		if o.namespaced && o.u.GetNamespace() == "" {
			o.u.SetNamespace(env.Namespace)
		}
		if !o.namespaced {
			o.u.SetNamespace("")
		}
		key := refOf(o.u).key()
		if seen[key] {
			refusals = append(refusals, o.String()+": rendered twice")
			continue
		}
		seen[key] = true
		labels := o.u.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[EnvironmentLabel] = env.ID
		o.u.SetLabels(labels)
		accepted = append(accepted, o)
	}
	return accepted, refusals
}

// refused is why env can't take o, or "".
func refused(env Environment, o object) string {
	gvk := o.u.GroupVersionKind()
	switch {
	case gvk.Group == "" && gvk.Kind == "Namespace":
		return "the environment's namespace is the environment's own; a render may not hold a Namespace"
	case o.namespaced && o.u.GetNamespace() != "" && o.u.GetNamespace() != env.Namespace:
		return "it names a namespace other than the environment's, " + env.Namespace
	case !o.namespaced && env.Own:
		return "it is cluster-scoped, and this installation's cluster takes namespaced objects only"
	}
	return ""
}

// ownership refuses each object that already exists in the cluster and isn't env's: one Cobranch didn't create, or
// another environment's. Cobranch never adopts what it didn't create.
func (c *Cluster) ownership(ctx context.Context, env Environment, objs []object) ([]string, error) {
	var refusals []string
	for _, o := range objs {
		if o.pending {
			continue
		}
		existing, err := c.client(o.resource, o.namespaced, o.u.GetNamespace()).Get(ctx, o.u.GetName(), metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			continue
		case apierrors.IsForbidden(err):
			refusals = append(refusals, o.String()+": the delivery's credentials may not read it")
			continue
		case err != nil:
			return nil, fmt.Errorf("reading %s: %w", o, err)
		}
		switch owner := existing.GetLabels()[EnvironmentLabel]; owner {
		case env.ID:
		case "":
			refusals = append(refusals, o.String()+": it already exists, and Cobranch didn't create it")
		default:
			refusals = append(refusals, o.String()+": it belongs to another environment, "+owner)
		}
	}
	return refusals, nil
}

// client is the dynamic client of resource, in namespace when it is namespaced.
func (c *Cluster) client(resource schema.GroupVersionResource, namespaced bool, namespace string) dynamic.ResourceInterface {
	if namespaced {
		return c.dynamic.Resource(resource).Namespace(namespace)
	}
	return c.dynamic.Resource(resource)
}

// order sorts objs as they apply: CRDs first, then by kind in Helm's install order, then kinds it doesn't name, by
// name; objects of one kind keep the render's order.
func order(objs []object) {
	rank := func(o object) int {
		gvk := o.u.GroupVersionKind()
		if isCRD(gvk.GroupKind()) {
			return -1
		}
		if i := slices.Index(releaseutil.InstallOrder, gvk.Kind); i >= 0 {
			return i
		}
		return len(releaseutil.InstallOrder)
	}
	slices.SortStableFunc(objs, func(a, b object) int {
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra - rb
		}
		if rank(a) == len(releaseutil.InstallOrder) {
			return strings.Compare(a.u.GetKind(), b.u.GetKind())
		}
		return 0
	})
}
