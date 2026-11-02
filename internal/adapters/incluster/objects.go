package incluster

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

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
