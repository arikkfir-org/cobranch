// Package jobcluster runs the job controller's runs in Kubernetes: runner.Cluster on client-go. Each organization's
// runs execute in a namespace of their own, which the controller creates with the organization's first run, with no
// ingress, egress only to DNS, the internet and the installation's own servers, the operator's defaults and quota, and
// a RoleBinding that lets the controller run pods there
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "Job namespaces").
package jobcluster

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"cobranch.dev/internal/services/runner"
)

var tracer = otel.Tracer("cobranch.dev/internal/adapters/jobcluster")

// The labels the controller writes on what it creates.
const (
	// labelInstallation marks a job namespace as one installation's, so that the installation lists only its own.
	labelInstallation = "cobranch.dev/installation"
	// labelOrganization names the organization whose runs a job namespace holds.
	labelOrganization = "cobranch.dev/organization"
	// labelRun names the run a pod is, by its ID.
	labelRun = "cobranch.dev/run"
	// labelManagedBy says Cobranch made it.
	labelManagedBy = "app.kubernetes.io/managed-by"
)

// fieldManager is the controller's name in what it applies.
const fieldManager = "cobranch-controller"

// prepared is how long a namespace the controller prepared stays prepared before the next run in it prepares it again,
// so that a change to the installation's settings reaches every namespace.
const prepared = 10 * time.Minute

// Config is the installation's settings for the job namespaces and the pods in them, from the chart's jobs values.
type Config struct {
	// Prefix starts every job namespace's name, which the organization's ID ends.
	Prefix string
	// Installation labels the job namespaces as this installation's.
	Installation string
	// PodSecurity is the Pod Security Standard level the namespaces enforce: restricted, baseline or privileged.
	PodSecurity string
	// Labels are more labels the namespaces carry, the operator's.
	Labels map[string]string
	// Home is the installation's namespace, where the controller's ServiceAccount, Controller, is, and its servers,
	// whose pods Release labels as its instance.
	Home, Controller, Release string
	// RunnerRole is the ClusterRole each namespace's RoleBinding grants the controller: pods, their logs, Secrets and
	// the namespace's guards.
	RunnerRole string
	// OwnerRole is the controller's own ClusterRole, which every job namespace names as its owner, so that deleting
	// the installation deletes them.
	OwnerRole string
	// EgressExcept are the address ranges the runs may not reach: private, link-local and shared ones, cloud metadata
	// servers among them.
	EgressExcept []string
	// DNS are the peers the runs may ask DNS, on port 53; any address on that port without them, which reaches the
	// cluster's DNS however it is set up.
	DNS []networkingv1.NetworkPolicyPeer
	// Resources are a container's defaults, its requests and limits, where it names none.
	Resources corev1.ResourceRequirements
	// Quota is the most the runs of one organization take at once; none without it.
	Quota corev1.ResourceList
	// NodeSelector, Tolerations and Affinity place the runs' pods.
	NodeSelector map[string]string
	Tolerations  []corev1.Toleration
	Affinity     *corev1.Affinity
	// CheckoutImage is the image whose `cobranch checkout` fetches a run's commit: Cobranch's own. An agent task's pod
	// copies the cobranch binary from it, and runs its model proxy in it.
	CheckoutImage string
	// AgentImage is the image an agent's harness runs in, with the tools its agents use; AgentResources are its
	// requests and limits, the namespace's defaults where it names none.
	AgentImage     string
	AgentResources corev1.ResourceRequirements
	// ImagePullSecrets name the Secrets in Home that pull the agents' and Cobranch's images, which Prepare copies into
	// every job namespace and an agent task's pod names.
	ImagePullSecrets []string
	// ProviderURLs are where a task's model proxy sends its provider's requests, by provider, when not to the
	// provider's own address.
	ProviderURLs map[string]string
}

// Cluster is runner.Cluster on a Kubernetes cluster.
type Cluster struct {
	client kubernetes.Interface
	cfg    Config
	now    func() time.Time

	mu       sync.Mutex
	prepared map[uuid.UUID]time.Time
}

var _ runner.Cluster = (*Cluster)(nil)

// New returns a Cluster that runs pods through client, as cfg says.
func New(client kubernetes.Interface, cfg Config) *Cluster {
	return &Cluster{client: client, cfg: cfg, now: time.Now, prepared: map[uuid.UUID]time.Time{}}
}

// namespace is the name of the organization's job namespace.
func (c *Cluster) namespace(orgID uuid.UUID) string { return c.cfg.Prefix + orgID.String() }

// podName is the name of a run's pod, and of its Secret.
func podName(runID uuid.UUID) string { return "run-" + runID.String() }

// Namespaces lists the organizations that have a job namespace of this installation (runner.Cluster).
func (c *Cluster) Namespaces(ctx context.Context) (_ []uuid.UUID, err error) {
	ctx, span := tracer.Start(ctx, "jobcluster.Cluster.Namespaces")
	defer span.End()
	defer func() { record(span, err) }()
	list, err := c.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: labelInstallation + "=" + c.cfg.Installation})
	if err != nil {
		return nil, fmt.Errorf("listing the job namespaces: %w", err)
	}
	out := make([]uuid.UUID, 0, len(list.Items))
	for _, ns := range list.Items {
		if id, err := uuid.Parse(strings.TrimPrefix(ns.Name, c.cfg.Prefix)); err == nil && strings.HasPrefix(ns.Name, c.cfg.Prefix) {
			out = append(out, id)
		}
	}
	return out, nil
}

// Drop deletes the organization's job namespace, and everything in it (runner.Cluster).
func (c *Cluster) Drop(ctx context.Context, orgID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "jobcluster.Cluster.Drop", orgAttr(orgID))
	defer span.End()
	defer func() { record(span, err) }()
	c.forget(orgID)
	err = c.client.CoreV1().Namespaces().Delete(ctx, c.namespace(orgID), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting namespace %s: %w", c.namespace(orgID), err)
	}
	return nil
}

// fresh reports whether the organization's namespace was prepared lately.
func (c *Cluster) fresh(orgID uuid.UUID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	at, ok := c.prepared[orgID]
	return ok && c.now().Sub(at) < prepared
}

// remember records that the organization's namespace was prepared now.
func (c *Cluster) remember(orgID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prepared[orgID] = c.now()
}

// forget has the organization's next run prepare its namespace again: it was deleted, or a run found it missing.
func (c *Cluster) forget(orgID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.prepared, orgID)
}

// orgAttr is a span's attribute naming the organization.
func orgAttr(orgID uuid.UUID) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String("cobranch.org.id", orgID.String()))
}

// runAttrs are a span's attributes naming the run and its organization.
func runAttrs(orgID, runID uuid.UUID) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String("cobranch.org.id", orgID.String()), attribute.String("cobranch.run.id", runID.String()))
}

// record marks span failed with err, if any.
func record(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}
