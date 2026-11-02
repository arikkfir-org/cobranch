package incluster

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"cobranch.dev/internal/adapters/cluster"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/deliverer"
)

// guards are the kinds that fence an environment's namespace, which only the controller writes: a render that holds
// one would widen what the namespace reaches, or lift its defaults and quota.
var guards = map[schema.GroupKind]string{
	{Group: "networking.k8s.io", Kind: "NetworkPolicy"}: "NetworkPolicies",
	{Kind: "LimitRange"}:    "LimitRanges",
	{Kind: "ResourceQuota"}: "ResourceQuotas",
}

// guarded refuses a render that holds any of the guards' kinds, naming each such object.
func guarded(rendered []*unstructured.Unstructured) error {
	var refusals []string
	for _, u := range rendered {
		if kinds, ok := guards[u.GroupVersionKind().GroupKind()]; ok {
			refusals = append(refusals, u.GetKind()+"/"+u.GetName()+": "+kinds+" fence the environment's namespace, "+
				"and only Cobranch writes them in this installation's cluster")
		}
	}
	if len(refusals) > 0 {
		return fmt.Errorf("%w: %s", deliverer.ErrRefused, strings.Join(refusals, "; "))
	}
	return nil
}

// refusal is why the cluster refused a render, without the error's own name.
func refusal(err error) string {
	return strings.TrimPrefix(err.Error(), cluster.ErrRefused.Error()+": ")
}

// environment is env as the cluster adapter applies for it, in the installation's own cluster.
func environment(env deliverer.Environment) cluster.Environment {
	return cluster.Environment{ID: env.ID.String(), Namespace: env.Namespace, Own: true}
}

// objectOf is the inventory's object of r, its health not read.
func objectOf(r cluster.Ref) deliverer.Object {
	return deliverer.Object{Group: r.Group, Version: r.Version, Kind: r.Kind, Namespace: r.Namespace, Name: r.Name, UID: r.UID,
		Health: code.Unknown}
}

// objectsOf is the inventory's objects of refs, in order.
func objectsOf(refs []cluster.Ref) []deliverer.Object {
	out := make([]deliverer.Object, 0, len(refs))
	for _, r := range refs {
		out = append(out, objectOf(r))
	}
	return out
}

// refsOf is the cluster's refs of objects, in order.
func refsOf(objects []deliverer.Object) []cluster.Ref {
	out := make([]cluster.Ref, 0, len(objects))
	for _, o := range objects {
		out = append(out, cluster.Ref{Group: o.Group, Version: o.Version, Kind: o.Kind, Namespace: o.Namespace, Name: o.Name, UID: o.UID})
	}
	return out
}

// healthOf is h as the Console's environments say it.
func healthOf(h cluster.Health) code.Health {
	return code.Health(strings.ToLower(string(h)))
}
