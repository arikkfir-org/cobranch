package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Prune deletes what previous holds and current doesn't, last applied first, when it still carries env's label: the
// objects a render stopped holding. A teardown prunes with no current inventory. It never deletes a CRD, which would
// delete every object of its kind in the cluster, nor an object that something replaced after it read it, and an
// object already gone counts as deleted, so a prune is safe to repeat. It carries on past an object it fails to
// delete, and returns every failure.
func (c *Cluster) Prune(ctx context.Context, env Environment, previous, current []Ref) error {
	ctx, span := tracer.Start(ctx, "cluster.Prune", trace.WithAttributes(attribute.String("cobranch.environment.id", env.ID),
		attribute.Int("cobranch.delivery.previous", len(previous)), attribute.Int("cobranch.delivery.current", len(current))))
	defer span.End()
	keep := map[string]bool{}
	for _, r := range current {
		keep[r.key()] = true
	}
	var errs []error
	for _, r := range slices.Backward(previous) {
		if keep[r.key()] || isCRD(r.gvk().GroupKind()) {
			continue
		}
		if err := c.prune(ctx, env, r); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return failed(span, fmt.Errorf("pruning %d of %d objects: %w", len(errs), len(previous), err))
	}
	return nil
}

// prune deletes r, if it is still there and still env's.
func (c *Cluster) prune(ctx context.Context, env Environment, r Ref) error {
	m, err := c.mapping(r)
	if meta.IsNoMatchError(err) {
		return nil // the cluster no longer serves its kind, so it is gone
	} else if err != nil {
		return err
	}
	client := c.client(m.Resource, m.Scope.Name() == meta.RESTScopeNameNamespace, r.Namespace)
	existing, err := client.Get(ctx, r.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("reading %s: %w", r, err)
	}
	if existing.GetLabels()[EnvironmentLabel] != env.ID {
		return nil
	}
	uid := existing.GetUID()
	background := metav1.DeletePropagationBackground
	err = client.Delete(ctx, r.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: (*types.UID)(&uid)},
		PropagationPolicy: &background})
	if apierrors.IsNotFound(err) || apierrors.IsConflict(err) { // gone, or replaced since it was read
		return nil
	}
	if err != nil {
		return fmt.Errorf("deleting %s: %w", r, err)
	}
	return nil
}

// mapping is r's kind's resource and scope, at r's version, or at the version the cluster prefers once it no longer
// serves that one.
func (c *Cluster) mapping(r Ref) (*meta.RESTMapping, error) {
	m, err := c.mapper.RESTMapping(r.gvk().GroupKind(), r.Version)
	if meta.IsNoMatchError(err) {
		m, err = c.mapper.RESTMapping(r.gvk().GroupKind())
	}
	if err != nil {
		return nil, fmt.Errorf("looking up %s's kind: %w", r, err)
	}
	return m, nil
}
