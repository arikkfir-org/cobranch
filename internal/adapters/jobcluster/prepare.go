package jobcluster

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/netip"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	networkingv1ac "k8s.io/client-go/applyconfigurations/networking/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/kubernetes"
)

// The objects the controller keeps in every job namespace, by name.
const (
	policyDenyIngress = "cobranch-deny-ingress"
	policyEgress      = "cobranch-egress"
	limitRange        = "cobranch-defaults"
	resourceQuota     = "cobranch-quota"
)

// serverComponents are the installation's servers a run may call: the REST and GraphQL servers with its token, and
// the Git server for its checkout.
var serverComponents = []string{"rest-server", "graphql-server", "git-server"}

// serverPort is the port the servers listen on.
const serverPort = 8080

// How long Prepare waits for its RoleBinding to take effect, and how often it tries meanwhile.
const (
	bindWait  = 10 * time.Second
	bindRetry = 250 * time.Millisecond
)

// Prepare makes the organization's job namespace ready for runs, creating or updating it and what it holds, unless it
// did so lately (runner.Cluster).
func (c *Cluster) Prepare(ctx context.Context, orgID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "jobcluster.Cluster.Prepare", orgAttr(orgID))
	defer span.End()
	defer func() { record(span, err) }()
	if c.fresh(orgID) {
		return nil
	}
	ns, apply := c.namespace(orgID), metav1.ApplyOptions{FieldManager: fieldManager, Force: true}
	core, rbac, networking := c.client.CoreV1(), c.client.RbacV1(), c.client.NetworkingV1()
	owner, err := c.owner(ctx)
	if err != nil {
		return err
	}
	if _, err := core.Namespaces().Apply(ctx, c.namespaceOf(orgID).WithOwnerReferences(owner), apply); err != nil {
		return fmt.Errorf("applying namespace %s: %w", ns, err)
	}
	if _, err := rbac.RoleBindings(ns).Apply(ctx, c.binding(ns), apply); err != nil {
		return fmt.Errorf("binding the controller in namespace %s: %w", ns, err)
	}
	egress, err := c.egress(ns)
	if err != nil {
		return err
	}
	// The binding takes a moment to reach the API server's authorizer: until it does, the first write is forbidden.
	err = c.untilBound(ctx, func() error {
		_, err := networking.NetworkPolicies(ns).Apply(ctx, denyIngress(ns), apply)
		return err //nolint:wrapcheck // wrapped below, once retries are over
	})
	if err != nil {
		return fmt.Errorf("applying NetworkPolicy %s in namespace %s: %w", policyDenyIngress, ns, err)
	}
	if _, err := networking.NetworkPolicies(ns).Apply(ctx, egress, apply); err != nil {
		return fmt.Errorf("applying NetworkPolicy %s in namespace %s: %w", policyEgress, ns, err)
	}
	if _, err := core.LimitRanges(ns).Apply(ctx, c.limits(ns), apply); err != nil {
		return fmt.Errorf("applying the defaults in namespace %s: %w", ns, err)
	}
	if err := c.quota(ctx, ns, apply); err != nil {
		return err
	}
	c.remember(orgID)
	return nil
}

// namespaceOf is the organization's job namespace: labelled as the installation's and the organization's, enforcing
// the installation's Pod Security level, with the operator's labels.
func (c *Cluster) namespaceOf(orgID uuid.UUID) *corev1ac.NamespaceApplyConfiguration {
	labels := maps.Clone(c.cfg.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	maps.Copy(labels, map[string]string{
		labelManagedBy: "cobranch", labelInstallation: c.cfg.Installation, labelOrganization: orgID.String(),
		"pod-security.kubernetes.io/enforce": c.cfg.PodSecurity, "pod-security.kubernetes.io/enforce-version": "latest",
	})
	return corev1ac.Namespace(c.namespace(orgID)).WithLabels(labels)
}

// owner is the reference every job namespace carries to the installation's OwnerRole, read afresh so that it names
// the role that exists now: uninstalling the installation deletes that role, and the garbage collector then deletes
// the namespaces and the runs in them, with no controller left to clean up. Without blockOwnerDeletion, setting it
// needs no access to the role beyond reading it.
func (c *Cluster) owner(ctx context.Context) (*metav1ac.OwnerReferenceApplyConfiguration, error) {
	role, err := c.client.RbacV1().ClusterRoles().Get(ctx, c.cfg.OwnerRole, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("reading ClusterRole %s, which owns the job namespaces: %w", c.cfg.OwnerRole, err)
	}
	return metav1ac.OwnerReference().WithAPIVersion("rbac.authorization.k8s.io/v1").WithKind("ClusterRole").
		WithName(role.Name).WithUID(role.UID), nil
}

// binding grants the controller the runner role in namespace ns.
func (c *Cluster) binding(ns string) *rbacv1ac.RoleBindingApplyConfiguration {
	return rbacv1ac.RoleBinding(c.cfg.RunnerRole, ns).WithLabels(map[string]string{labelManagedBy: "cobranch"}).
		WithRoleRef(rbacv1ac.RoleRef().WithAPIGroup("rbac.authorization.k8s.io").WithKind("ClusterRole").WithName(c.cfg.RunnerRole)).
		WithSubjects(rbacv1ac.Subject().WithKind("ServiceAccount").WithNamespace(c.cfg.Home).WithName(c.cfg.Controller))
}

// denyIngress admits nothing to the namespace's pods.
func denyIngress(ns string) *networkingv1ac.NetworkPolicyApplyConfiguration {
	return networkingv1ac.NetworkPolicy(policyDenyIngress, ns).WithLabels(map[string]string{labelManagedBy: "cobranch"}).
		WithSpec(networkingv1ac.NetworkPolicySpec().WithPodSelector(metav1ac.LabelSelector()).WithPolicyTypes("Ingress"))
}

// egress lets the namespace's pods reach DNS, every address outside EgressExcept, and the installation's servers.
func (c *Cluster) egress(ns string) (*networkingv1ac.NetworkPolicyApplyConfiguration, error) {
	dns, err := DNSEgress(c.cfg.DNS)
	if err != nil {
		return nil, err
	}
	internet, err := Internet(c.cfg.EgressExcept)
	if err != nil {
		return nil, fmt.Errorf("jobs' %w", err)
	}
	servers := networkingv1ac.NetworkPolicyEgressRule().
		WithTo(networkingv1ac.NetworkPolicyPeer().
			WithNamespaceSelector(metav1ac.LabelSelector().WithMatchLabels(map[string]string{"kubernetes.io/metadata.name": c.cfg.Home})).
			WithPodSelector(metav1ac.LabelSelector().WithMatchLabels(map[string]string{"app.kubernetes.io/instance": c.cfg.Release}).
				WithMatchExpressions(metav1ac.LabelSelectorRequirement().WithKey("app.kubernetes.io/component").
					WithOperator(metav1.LabelSelectorOpIn).WithValues(serverComponents...)))).
		WithPorts(networkingv1ac.NetworkPolicyPort().WithProtocol(corev1.ProtocolTCP).WithPort(intstr.FromInt32(serverPort)))
	return networkingv1ac.NetworkPolicy(policyEgress, ns).WithLabels(map[string]string{labelManagedBy: "cobranch"}).
		WithSpec(networkingv1ac.NetworkPolicySpec().WithPodSelector(metav1ac.LabelSelector()).WithPolicyTypes("Egress").
			WithEgress(dns, internet, servers)), nil
}

// DNSEgress lets a namespace's pods ask DNS on port 53: of the peers dns names, or of any address without them, which
// reaches the cluster's DNS however it is set up. The job namespaces and the environment namespaces both take it.
func DNSEgress(dns []networkingv1.NetworkPolicyPeer) (*networkingv1ac.NetworkPolicyEgressRuleApplyConfiguration, error) {
	rule := networkingv1ac.NetworkPolicyEgressRule().WithPorts(
		networkingv1ac.NetworkPolicyPort().WithProtocol(corev1.ProtocolUDP).WithPort(intstr.FromInt32(53)),
		networkingv1ac.NetworkPolicyPort().WithProtocol(corev1.ProtocolTCP).WithPort(intstr.FromInt32(53)))
	for _, peer := range dns {
		applied, err := PeerOf(peer)
		if err != nil {
			return nil, err
		}
		rule = rule.WithTo(applied)
	}
	return rule, nil
}

// Internet lets a namespace's pods reach every IPv4 and IPv6 address but except's ranges, each family's block
// excepting its own.
func Internet(except []string) (*networkingv1ac.NetworkPolicyEgressRuleApplyConfiguration, error) {
	var v4, v6 []string
	for _, cidr := range except {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, fmt.Errorf("egress excepts %q, which is no address range: %w", cidr, err)
		}
		if prefix.Addr().Is4() {
			v4 = append(v4, prefix.Masked().String())
		} else {
			v6 = append(v6, prefix.Masked().String())
		}
	}
	return networkingv1ac.NetworkPolicyEgressRule().WithTo(
		networkingv1ac.NetworkPolicyPeer().WithIPBlock(networkingv1ac.IPBlock().WithCIDR("0.0.0.0/0").WithExcept(v4...)),
		networkingv1ac.NetworkPolicyPeer().WithIPBlock(networkingv1ac.IPBlock().WithCIDR("::/0").WithExcept(v6...))), nil
}

// limits gives the namespace's containers the installation's defaults.
func (c *Cluster) limits(ns string) *corev1ac.LimitRangeApplyConfiguration {
	return Limits(ns, c.cfg.Resources)
}

// Limits is the LimitRange that gives namespace ns's containers r's requests and limits where they name none.
func Limits(ns string, r corev1.ResourceRequirements) *corev1ac.LimitRangeApplyConfiguration {
	item := corev1ac.LimitRangeItem().WithType(corev1.LimitTypeContainer)
	if len(r.Limits) > 0 {
		item = item.WithDefault(r.Limits)
	}
	if len(r.Requests) > 0 {
		item = item.WithDefaultRequest(r.Requests)
	}
	return corev1ac.LimitRange(limitRange, ns).WithLabels(map[string]string{labelManagedBy: "cobranch"}).
		WithSpec(corev1ac.LimitRangeSpec().WithLimits(item))
}

// quota applies the installation's quota to namespace ns, or deletes the one there when it has none.
func (c *Cluster) quota(ctx context.Context, ns string, apply metav1.ApplyOptions) error {
	return ApplyQuota(ctx, c.client, ns, c.cfg.Quota, apply)
}

// ApplyQuota applies hard as namespace ns's ResourceQuota, or deletes the one there when hard is empty.
func ApplyQuota(ctx context.Context, client kubernetes.Interface, ns string, hard corev1.ResourceList, apply metav1.ApplyOptions) error {
	quotas := client.CoreV1().ResourceQuotas(ns)
	if len(hard) == 0 {
		if err := quotas.Delete(ctx, resourceQuota, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting the quota in namespace %s: %w", ns, err)
		}
		return nil
	}
	q := corev1ac.ResourceQuota(resourceQuota, ns).WithLabels(map[string]string{labelManagedBy: "cobranch"}).
		WithSpec(corev1ac.ResourceQuotaSpec().WithHard(hard))
	if _, err := quotas.Apply(ctx, q, apply); err != nil {
		return fmt.Errorf("applying the quota in namespace %s: %w", ns, err)
	}
	return nil
}

// PeerOf is a NetworkPolicy peer as an apply configuration, which has its JSON.
func PeerOf(peer networkingv1.NetworkPolicyPeer) (*networkingv1ac.NetworkPolicyPeerApplyConfiguration, error) {
	encoded, err := json.Marshal(peer)
	if err != nil {
		return nil, fmt.Errorf("encoding a NetworkPolicy peer: %w", err)
	}
	var applied networkingv1ac.NetworkPolicyPeerApplyConfiguration
	if err := json.Unmarshal(encoded, &applied); err != nil {
		return nil, fmt.Errorf("decoding a NetworkPolicy peer: %w", err)
	}
	return &applied, nil
}

// untilBound calls write until the controller's new binding lets it.
func (c *Cluster) untilBound(ctx context.Context, write func() error) error {
	return UntilBound(ctx, c.now, write)
}

// UntilBound calls write until a new RoleBinding lets it, as RBAC's caches take the binding in: again while it is
// forbidden, every bindRetry, for bindWait at most.
func UntilBound(ctx context.Context, now func() time.Time, write func() error) error {
	deadline := now().Add(bindWait)
	for {
		err := write()
		if !apierrors.IsForbidden(err) || !now().Before(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for a binding: %w", ctx.Err())
		case <-time.After(bindRetry):
		}
	}
}
