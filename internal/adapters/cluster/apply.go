package cluster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

// crds is CustomResourceDefinition's resource.
var crds = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

// Decode reads a render, a stream of YAML documents each one object, as `cobranch render` writes it.
func Decode(render []byte) ([]*unstructured.Unstructured, error) {
	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(render), 4096)
	var out []*unstructured.Unstructured
	for {
		var o map[string]any
		if err := dec.Decode(&o); errors.Is(err, io.EOF) {
			return out, nil
		} else if err != nil {
			return nil, fmt.Errorf("decoding the render's object %d: %w", len(out)+1, err)
		}
		if len(o) > 0 {
			out = append(out, &unstructured.Unstructured{Object: o})
		}
	}
}

// Apply delivers rendered to env: it checks every object first and, when one is refused (ErrRefused), applies
// nothing. Then it server-side applies each, forcing conflicts, CRDs first, waiting for them to be Established, and the
// rest in Helm's install order, and returns what it applied, in order: the environment's new inventory, which it
// records before it prunes the previous one's leftovers. When an apply fails partway, it returns what it had applied
// with the error, which the inventory adds to the previous one's, so nothing applied is ever forgotten.
func (c *Cluster) Apply(ctx context.Context, env Environment, rendered []*unstructured.Unstructured) ([]Ref, error) {
	ctx, span := tracer.Start(ctx, "cluster.Apply", trace.WithAttributes(
		attribute.String("cobranch.environment.id", env.ID), attribute.Int("cobranch.delivery.objects", len(rendered))))
	defer span.End()
	refs, err := c.apply(ctx, env, rendered)
	if err != nil {
		return refs, failed(span, err)
	}
	return refs, nil
}

// apply is Apply without its span.
func (c *Cluster) apply(ctx context.Context, env Environment, rendered []*unstructured.Unstructured) ([]Ref, error) {
	objs, err := c.checked(ctx, env, rendered)
	if err != nil {
		return nil, err
	}
	order(objs)
	refs := make([]Ref, 0, len(objs))
	waited := false
	for _, o := range objs {
		if !waited && !isCRD(o.u.GroupVersionKind().GroupKind()) {
			if err := c.established(ctx, refs); err != nil {
				return refs, err
			}
			waited = true
		}
		applied, err := c.client(o.resource, o.namespaced, o.u.GetNamespace()).Apply(ctx, o.u.GetName(), o.u,
			metav1.ApplyOptions{FieldManager: FieldManager, Force: true})
		if err != nil {
			return refs, fmt.Errorf("applying %s: %w", o, err)
		}
		refs = append(refs, refOf(applied))
	}
	if !waited {
		return refs, c.established(ctx, refs)
	}
	return refs, nil
}

// checked resolves and checks every rendered object, or refuses the render naming each object it can't take.
func (c *Cluster) checked(ctx context.Context, env Environment, rendered []*unstructured.Unstructured) ([]object, error) {
	objs, refusals, err := c.resolve(rendered)
	if err != nil {
		return nil, err
	}
	objs, refused := check(env, objs)
	refusals = append(refusals, refused...)
	owned, err := c.ownership(ctx, env, objs)
	if err != nil {
		return nil, err
	}
	if refusals = append(refusals, owned...); len(refusals) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrRefused, strings.Join(refusals, "; "))
	}
	return objs, nil
}

// established waits until each CRD among applied is Established, for at most c.Establish in all, and then forgets
// what the cluster served before, so the kinds they define resolve.
func (c *Cluster) established(ctx context.Context, applied []Ref) error {
	var names []string
	for _, r := range applied {
		if isCRD(r.gvk().GroupKind()) {
			names = append(names, r.Name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	err := wait.PollUntilContextTimeout(ctx, c.poll, c.Establish, true, func(ctx context.Context) (bool, error) {
		for _, name := range names {
			crd, err := c.dynamic.Resource(crds).Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return false, nil
			} else if err != nil {
				return false, fmt.Errorf("reading CustomResourceDefinition/%s: %w", name, err)
			}
			if !condition(crd, "Established") {
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for CustomResourceDefinitions %s to be Established: %w", strings.Join(names, ", "), err)
	}
	c.mapper.Reset()
	return nil
}

// condition reports whether u's status holds condition kind, True.
func condition(u *unstructured.Unstructured, kind string) bool {
	conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions") // none holds none
	for _, c := range conditions {
		m, _ := c.(map[string]any) // a condition that isn't a mapping is no condition
		if text(m, "type") == kind && text(m, "status") == "True" {
			return true
		}
	}
	return false
}
