// Package incluster delivers into the installation's own cluster: deliverer.Target on client-go. Each environment gets
// a namespace the controller makes, labels as the installation's and the environment's, and fences with
// NetworkPolicies, a LimitRange and an optional ResourceQuota; the controller then applies, prunes and reads health as
// the delivery account, a ServiceAccount it impersonates, which a RoleBinding in that namespace alone lets do what the
// delivery role allows (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md, "This
// installation's cluster").
package incluster

import (
	"context"
	"errors"
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
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/deliverer"
)

var tracer = otel.Tracer("cobranch.dev/internal/adapters/incluster")

// The labels the controller writes on an environment's namespace.
const (
	// labelInstallation marks it as one installation's, as the job namespaces are.
	labelInstallation = "cobranch.dev/installation"
	// labelOrganization names the organization whose environment it holds.
	labelOrganization = "cobranch.dev/organization"
	// labelManagedBy says Cobranch made it.
	labelManagedBy = "app.kubernetes.io/managed-by"
)

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

// Target is deliverer.Target in the cluster the controller runs in.
type Target struct {
	// client is the controller's own, which makes and fences the namespaces; account the delivery account's, which
	// applies; cluster applies, prunes and reads health through account's configuration.
	client, account kubernetes.Interface
	cluster         *cluster.Cluster
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
	return &Target{client: client, account: account, cluster: c, cfg: cfg, now: time.Now}, nil
}

// Capabilities are the cluster's Kubernetes version and the API versions it serves, as the delivery account
// discovers them (deliverer.Target).
func (t *Target) Capabilities(ctx context.Context) (string, []string, error) {
	version, apis, err := t.cluster.Capabilities(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("discovering the cluster: %w", err)
	}
	return version, apis, nil
}

// Apply applies manifests to env's namespace as the delivery account, refusing all of it when it holds an object the
// namespace can't take (deliverer.Target).
func (t *Target) Apply(ctx context.Context, env deliverer.Environment, manifests []byte) (_ []deliverer.Object, err error) {
	ctx, span := tracer.Start(ctx, "incluster.Target.Apply", envAttrs(env))
	defer span.End()
	defer func() { record(span, err) }()
	rendered, err := cluster.Decode(manifests)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", deliverer.ErrRefused, err)
	}
	if err := guarded(rendered); err != nil {
		return nil, err
	}
	refs, err := t.cluster.Apply(ctx, environment(env), rendered)
	objects := objectsOf(refs)
	if errors.Is(err, cluster.ErrRefused) {
		return objects, fmt.Errorf("%w: %s", deliverer.ErrRefused, refusal(err))
	}
	if err != nil {
		return objects, fmt.Errorf("applying to namespace %s: %w", env.Namespace, err)
	}
	return objects, nil
}

// Prune deletes what previous holds and current doesn't, as the delivery account (deliverer.Target).
func (t *Target) Prune(ctx context.Context, env deliverer.Environment, previous, current []deliverer.Object) error {
	if err := t.cluster.Prune(ctx, environment(env), refsOf(previous), refsOf(current)); err != nil {
		return fmt.Errorf("pruning namespace %s: %w", env.Namespace, err)
	}
	return nil
}

// Health reads the objects' healths as the delivery account, in order, and the worst of them (deliverer.Target).
func (t *Target) Health(ctx context.Context, objects []deliverer.Object) ([]deliverer.Object, code.Health, error) {
	read, worst, err := t.cluster.Health(ctx, refsOf(objects))
	if err != nil {
		return nil, "", fmt.Errorf("reading health: %w", err)
	}
	out := make([]deliverer.Object, 0, len(read))
	for _, h := range read {
		o := objectOf(h.Ref)
		o.Health, o.Message = healthOf(h.Health), h.Message
		out = append(out, o)
	}
	return out, healthOf(worst), nil
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
