package remote

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"cobranch.dev/internal/adapters/cluster"
	"cobranch.dev/internal/services/deliverer"
)

// Prepare makes env's namespace when it is missing, labelled as the installation's and the environment's, or uses the
// one that is there, unless another environment's label says it is that one's (deliverer.Target). A kubeconfig that
// may not make a missing namespace fails the delivery, saying so.
func (t *Target) Prepare(ctx context.Context, env deliverer.Environment) (err error) {
	ctx, span := tracer.Start(ctx, "remote.Target.Prepare", envAttrs(env))
	defer span.End()
	defer func() { record(span, err) }()
	namespaces := t.client.CoreV1().Namespaces()
	existing, err := namespaces.Get(ctx, env.Namespace, metav1.GetOptions{})
	switch {
	case err == nil:
		if owner, ok := existing.Labels[cluster.EnvironmentLabel]; ok && owner != env.ID.String() {
			return fmt.Errorf("%w: namespace %s is another environment's", deliverer.ErrRefused, env.Namespace)
		}
		return nil
	case !apierrors.IsNotFound(err) && !apierrors.IsForbidden(err):
		return fmt.Errorf("reading namespace %s: %w", env.Namespace, err)
	}
	// Missing, or the kubeconfig may not read namespaces: making it tells which.
	_, err = namespaces.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: env.Namespace, Labels: map[string]string{
		cluster.ManagedByLabel: "cobranch", cluster.InstallationLabel: t.installation, cluster.OrganizationLabel: env.OrgID.String(),
		cluster.EnvironmentLabel: env.ID.String(),
	}}}, metav1.CreateOptions{FieldManager: cluster.FieldManager})
	switch {
	case err == nil, apierrors.IsAlreadyExists(err):
		return nil
	case apierrors.IsForbidden(err):
		return fmt.Errorf("%w: namespace %s is missing, and the kubeconfig may not make it", deliverer.ErrRefused, env.Namespace)
	}
	return fmt.Errorf("creating namespace %s: %w", env.Namespace, err)
}

// Release deletes env's namespace if Cobranch made it, and answers whether it is gone, or was never Cobranch's
// (deliverer.Target).
func (t *Target) Release(ctx context.Context, env deliverer.Environment) (_ bool, err error) {
	ctx, span := tracer.Start(ctx, "remote.Target.Release", envAttrs(env))
	defer span.End()
	defer func() { record(span, err) }()
	gone, err := cluster.Release(ctx, t.client.CoreV1().Namespaces(), env.Namespace, env.ID.String())
	if err != nil {
		return false, fmt.Errorf("releasing namespace %s: %w", env.Namespace, err)
	}
	return gone, nil
}

// record marks span failed with err, if any.
func record(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

// envAttrs are the attributes of a span about env.
func envAttrs(env deliverer.Environment) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String("cobranch.environment.id", env.ID.String()),
		attribute.String("cobranch.org.id", env.OrgID.String()), attribute.String("k8s.namespace.name", env.Namespace))
}
