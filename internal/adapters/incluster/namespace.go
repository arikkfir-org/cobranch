package incluster

import (
	"context"
	"fmt"
	"maps"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	networkingv1ac "k8s.io/client-go/applyconfigurations/networking/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"

	"cobranch.dev/internal/adapters/cluster"
	"cobranch.dev/internal/adapters/jobcluster"
	"cobranch.dev/internal/services/deliverer"
)

// The objects the controller keeps in every environment's namespace, by name.
const (
	policyIngress = "cobranch-ingress"
	policyEgress  = "cobranch-egress"
)

// Prepare makes env's namespace, or refuses one that exists and isn't env's; then it binds itself there to the fence
// role and the delivery account to the delivery role, fences the namespace, and waits until the account may apply
// there (deliverer.Target).
func (t *Target) Prepare(ctx context.Context, env deliverer.Environment) (err error) {
	ctx, span := tracer.Start(ctx, "incluster.Target.Prepare", envAttrs(env))
	defer span.End()
	defer func() { record(span, err) }()
	if err := t.namespace(ctx, env); err != nil {
		return err
	}
	ns, apply := env.Namespace, metav1.ApplyOptions{FieldManager: fieldManager, Force: true}
	rbac := t.client.RbacV1().RoleBindings(ns)
	if _, err := rbac.Apply(ctx, t.binding(ns, t.cfg.FenceRole, t.cfg.Controller), apply); err != nil {
		return fmt.Errorf("binding the controller in namespace %s: %w", ns, err)
	}
	if _, err := rbac.Apply(ctx, t.binding(ns, t.cfg.DeliveryRole, t.cfg.Account), apply); err != nil {
		return fmt.Errorf("binding the delivery account in namespace %s: %w", ns, err)
	}
	if err := t.fence(ctx, ns, apply); err != nil {
		return err
	}
	// The account's binding takes a moment to reach the API server's authorizer, as the controller's did.
	err = jobcluster.UntilBound(ctx, t.now, func() error {
		_, err := t.account.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{Limit: 1})
		return err //nolint:wrapcheck // wrapped below, once retries are over
	})
	if err != nil {
		return fmt.Errorf("waiting for the delivery account's binding in namespace %s: %w", ns, err)
	}
	return nil
}

// namespace creates env's namespace, or updates it when it is env's already, and refuses one that isn't: a repository
// can't deliver into kube-system, another installation's namespaces, or another environment's.
func (t *Target) namespace(ctx context.Context, env deliverer.Environment) error {
	if t.cfg.JobsPrefix != "" && strings.HasPrefix(env.Namespace, t.cfg.JobsPrefix) {
		return fmt.Errorf("%w: namespace %s starts with %s, which names this installation's job namespaces", deliverer.ErrRefused,
			env.Namespace, t.cfg.JobsPrefix)
	}
	namespaces := t.client.CoreV1().Namespaces()
	existing, err := namespaces.Get(ctx, env.Namespace, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err := namespaces.Create(ctx, t.namespaceOf(env), metav1.CreateOptions{FieldManager: fieldManager})
		if apierrors.IsAlreadyExists(err) {
			return t.namespace(ctx, env) // made meanwhile: look at whose it is
		}
		return wrapIf(err, "creating namespace "+env.Namespace)
	case err != nil:
		return fmt.Errorf("reading namespace %s: %w", env.Namespace, err)
	}
	if l := existing.Labels; l[cluster.InstallationLabel] != t.cfg.Installation || l[cluster.EnvironmentLabel] != env.ID.String() {
		return fmt.Errorf("%w: namespace %s exists, and isn't this environment's: Cobranch delivers only into namespaces it made",
			deliverer.ErrRefused, env.Namespace)
	}
	apply := corev1ac.Namespace(env.Namespace).WithLabels(t.labels(env))
	if _, err := namespaces.Apply(ctx, apply, metav1.ApplyOptions{FieldManager: fieldManager, Force: true}); err != nil {
		return fmt.Errorf("updating namespace %s: %w", env.Namespace, err)
	}
	return nil
}

// namespaceOf is env's namespace as the controller creates it.
func (t *Target) namespaceOf(env deliverer.Environment) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: env.Namespace, Labels: t.labels(env)}}
}

// labels are the labels of env's namespace: the installation's, the organization's and the environment's, the Pod
// Security level it enforces, and the operator's.
func (t *Target) labels(env deliverer.Environment) map[string]string {
	labels := maps.Clone(t.cfg.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	maps.Copy(labels, map[string]string{
		cluster.ManagedByLabel: "cobranch", cluster.InstallationLabel: t.cfg.Installation, cluster.OrganizationLabel: env.OrgID.String(),
		cluster.EnvironmentLabel:             env.ID.String(),
		"pod-security.kubernetes.io/enforce": t.cfg.PodSecurity, "pod-security.kubernetes.io/enforce-version": "latest",
	})
	return labels
}

// binding grants the ServiceAccount account of the installation's namespace the ClusterRole role in namespace ns, in
// a RoleBinding named after the role.
func (t *Target) binding(ns, role, account string) *rbacv1ac.RoleBindingApplyConfiguration {
	return rbacv1ac.RoleBinding(role, ns).WithLabels(map[string]string{cluster.ManagedByLabel: "cobranch"}).
		WithRoleRef(rbacv1ac.RoleRef().WithAPIGroup("rbac.authorization.k8s.io").WithKind("ClusterRole").WithName(role)).
		WithSubjects(rbacv1ac.Subject().WithKind("ServiceAccount").WithNamespace(t.cfg.Home).WithName(account))
}

// fence applies namespace ns's NetworkPolicies, LimitRange and quota, once the controller's binding there lets it.
func (t *Target) fence(ctx context.Context, ns string, apply metav1.ApplyOptions) error {
	egress, err := t.egress(ns)
	if err != nil {
		return err
	}
	ingress, err := t.ingress(ns)
	if err != nil {
		return err
	}
	policies := t.client.NetworkingV1().NetworkPolicies(ns)
	// The binding takes a moment to reach the API server's authorizer: until it does, the first write is forbidden.
	err = jobcluster.UntilBound(ctx, t.now, func() error {
		_, err := policies.Apply(ctx, ingress, apply)
		return err //nolint:wrapcheck // wrapped below, once retries are over
	})
	if err != nil {
		return fmt.Errorf("applying NetworkPolicy %s in namespace %s: %w", policyIngress, ns, err)
	}
	if _, err := policies.Apply(ctx, egress, apply); err != nil {
		return fmt.Errorf("applying NetworkPolicy %s in namespace %s: %w", policyEgress, ns, err)
	}
	if _, err := t.client.CoreV1().LimitRanges(ns).Apply(ctx, jobcluster.Limits(ns, t.cfg.Resources), apply); err != nil {
		return fmt.Errorf("applying the defaults in namespace %s: %w", ns, err)
	}
	return jobcluster.ApplyQuota(ctx, t.client, ns, t.cfg.Quota, apply) //nolint:wrapcheck // it names the namespace
}

// ingress admits to the namespace's pods only its own and those of the namespaces IngressFrom selects.
func (t *Target) ingress(ns string) (*networkingv1ac.NetworkPolicyApplyConfiguration, error) {
	rule := networkingv1ac.NetworkPolicyIngressRule().WithFrom(networkingv1ac.NetworkPolicyPeer().WithPodSelector(metav1ac.LabelSelector()))
	for _, selector := range t.cfg.IngressFrom {
		peer, err := jobcluster.PeerOf(networkingv1.NetworkPolicyPeer{NamespaceSelector: &selector})
		if err != nil {
			return nil, err //nolint:wrapcheck // it says what it encoded
		}
		rule = rule.WithFrom(peer)
	}
	return networkingv1ac.NetworkPolicy(policyIngress, ns).WithLabels(map[string]string{cluster.ManagedByLabel: "cobranch"}).
		WithSpec(networkingv1ac.NetworkPolicySpec().WithPodSelector(metav1ac.LabelSelector()).WithPolicyTypes("Ingress").
			WithIngress(rule)), nil
}

// egress lets the namespace's pods reach its own, DNS, and every address outside EgressExcept.
func (t *Target) egress(ns string) (*networkingv1ac.NetworkPolicyApplyConfiguration, error) {
	dns, err := jobcluster.DNSEgress(t.cfg.DNS)
	if err != nil {
		return nil, err //nolint:wrapcheck // it says what it encoded
	}
	internet, err := jobcluster.Internet(t.cfg.EgressExcept)
	if err != nil {
		return nil, fmt.Errorf("environments' %w", err)
	}
	own := networkingv1ac.NetworkPolicyEgressRule().WithTo(networkingv1ac.NetworkPolicyPeer().WithPodSelector(metav1ac.LabelSelector()))
	return networkingv1ac.NetworkPolicy(policyEgress, ns).WithLabels(map[string]string{cluster.ManagedByLabel: "cobranch"}).
		WithSpec(networkingv1ac.NetworkPolicySpec().WithPodSelector(metav1ac.LabelSelector()).WithPolicyTypes("Egress").
			WithEgress(own, dns, internet)), nil
}

// wrapIf wraps err with what was being done, or answers nil without one.
func wrapIf(err error, what string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", what, err)
}
