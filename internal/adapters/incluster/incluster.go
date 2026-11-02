// Package incluster delivers into the installation's own cluster: deliverer.Target on client-go. Each environment gets
// a namespace the controller makes, labels as the installation's and the environment's, and fences with
// NetworkPolicies, a LimitRange and an optional ResourceQuota; the controller then applies, prunes and reads health as
// the delivery account, a ServiceAccount it impersonates, which a RoleBinding in that namespace alone lets do what the
// delivery role allows (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md, "This
// installation's cluster").
package incluster

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"cobranch.dev/internal/adapters/cluster"
	"cobranch.dev/internal/services/deliverer"
)

var tracer = otel.Tracer("cobranch.dev/internal/adapters/incluster")

// fieldManager is the controller's name in what it applies to fence a namespace; what a render holds it applies as
// cluster.FieldManager.
const fieldManager = "cobranch-controller"

// Config is the installation's settings for its environments' namespaces, from the chart's delivery.inCluster values.
type Config struct {
	// Installation labels the namespaces as this installation's.
	Installation string
	// JobsPrefix starts the job namespaces' names, which an environment's namespace never takes: the controller tells
	// a job namespace by its name.
	JobsPrefix string
	// PodSecurity is the Pod Security Standard level the namespaces enforce: restricted, baseline or privileged.
	PodSecurity string
	// Labels are more labels the namespaces carry, the operator's.
	Labels map[string]string
	// Home is the installation's namespace, where the controller's ServiceAccount, Controller, is, and the delivery
	// account, Account, which the controller impersonates to apply.
	Home, Controller, Account string
	// DeliveryRole is the ClusterRole each namespace's RoleBinding grants the delivery account: what a render may
	// hold. FenceRole is the one another grants the controller there: the namespace's NetworkPolicies, LimitRange and
	// ResourceQuota, which the delivery role leaves out.
	DeliveryRole, FenceRole string
	// IngressFrom selects the namespaces whose pods may reach an environment's, such as the Gateway's; the
	// environment's own always may.
	IngressFrom []metav1.LabelSelector
	// EgressExcept are the address ranges an environment may not reach, as the jobs may not; DNS the peers it may ask
	// DNS, on port 53, any address without them.
	EgressExcept []string
	DNS          []networkingv1.NetworkPolicyPeer
	// Resources are a container's defaults, its requests and limits, where it names none.
	Resources corev1.ResourceRequirements
	// Quota is the most one environment takes at once; none without it.
	Quota corev1.ResourceList
}

// Target is deliverer.Target in the cluster the controller runs in: what it applies, prunes, reads and tears down, the
// embedded cluster.Target does through the delivery account's configuration.
type Target struct {
	*cluster.Target
	// client is the controller's own, which makes, fences and deletes the namespaces; account the delivery account's.
	client, account kubernetes.Interface
	cfg             Config
	now             func() time.Time
}

var _ deliverer.Target = (*Target)(nil)

// New returns the Target that kube, the controller's configuration, reaches, applying as cfg's delivery account.
func New(kube *rest.Config, cfg Config) (*Target, error) {
	client, err := kubernetes.NewForConfig(kube)
	if err != nil {
		return nil, fmt.Errorf("making the cluster's client: %w", err)
	}
	impersonated := rest.CopyConfig(kube)
	impersonated.Impersonate = rest.ImpersonationConfig{UserName: "system:serviceaccount:" + cfg.Home + ":" + cfg.Account}
	account, err := kubernetes.NewForConfig(impersonated)
	if err != nil {
		return nil, fmt.Errorf("making the delivery account's client: %w", err)
	}
	c, err := cluster.New(impersonated)
	if err != nil {
		return nil, fmt.Errorf("reaching the cluster as the delivery account: %w", err)
	}
	return &Target{Target: &cluster.Target{Cluster: c, Own: true, Guard: guarded}, client: client, account: account, cfg: cfg,
		now: time.Now}, nil
}

// Release deletes env's namespace, which the controller made, and answers whether it is gone (deliverer.Target).
func (t *Target) Release(ctx context.Context, env deliverer.Environment) (_ bool, err error) {
	ctx, span := tracer.Start(ctx, "incluster.Target.Release", envAttrs(env))
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
