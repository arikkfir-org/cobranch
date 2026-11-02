package cluster

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// Teardown deletes what refs, an environment's inventory, holds that is still env's, last applied first, and answers
// which of refs remain env's in the cluster: those finalizers still hold, or that it failed to delete. CRDs are never
// deleted, and never remain; nor does an object someone relabelled, or recreated since, which is no longer env's. A
// teardown is safe to repeat: an object already gone counts as deleted.
func (c *Cluster) Teardown(ctx context.Context, env Environment, refs []Ref) ([]Ref, error) {
	ctx, span := tracer.Start(ctx, "cluster.Teardown", trace.WithAttributes(attribute.String("cobranch.environment.id", env.ID),
		attribute.Int("cobranch.delivery.objects", len(refs))))
	defer span.End()
	pruned := c.Prune(ctx, env, refs, nil)
	var remaining []Ref
	var errs []error
	for _, r := range refs {
		if isCRD(r.gvk().GroupKind()) {
			continue
		}
		left, err := c.remains(ctx, env, r)
		if err != nil {
			errs = append(errs, err)
		}
		if left || err != nil {
			remaining = append(remaining, r)
		}
	}
	// What it couldn't delete, or read, remains, and the error says why.
	if err := errors.Join(append(errs, pruned)...); err != nil {
		return remaining, failed(span, err)
	}
	return remaining, nil
}

// remains reports whether r is still in the cluster and still env's.
func (c *Cluster) remains(ctx context.Context, env Environment, r Ref) (bool, error) {
	m, err := c.mapping(r)
	if meta.IsNoMatchError(err) {
		return false, nil // the cluster no longer serves its kind, so it is gone
	} else if err != nil {
		return false, err
	}
	u, err := c.client(m.Resource, m.Scope.Name() == meta.RESTScopeNameNamespace, r.Namespace).Get(ctx, r.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("reading %s: %w", r, err)
	}
	return u.GetLabels()[EnvironmentLabel] == env.ID && string(u.GetUID()) == r.UID, nil
}

// Release deletes the environment's namespace, name, when it carries the environment's label, id, as the namespaces
// Cobranch makes do, and answers whether it is gone or was never Cobranch's: one that is deleting, as its finalizers
// run, isn't gone yet.
func Release(ctx context.Context, namespaces corev1client.NamespaceInterface, name, id string) (bool, error) {
	ns, err := namespaces.Get(ctx, name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("reading namespace %s: %w", name, err)
	case ns.Labels[EnvironmentLabel] != id:
		return true, nil // someone else's: Cobranch didn't make it, or no longer owns it
	case ns.DeletionTimestamp != nil:
		return false, nil
	}
	uid := ns.UID
	err = namespaces.Delete(ctx, name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
	switch {
	case apierrors.IsNotFound(err):
		return true, nil
	case apierrors.IsConflict(err):
		return false, nil // replaced since it was read: look again
	case err != nil:
		return false, fmt.Errorf("deleting namespace %s: %w", name, err)
	}
	return false, nil
}
