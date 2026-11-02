// Package cluster delivers a render to its target cluster and reads back what it delivered: every object is checked
// before the first is applied, then server-side applied as Cobranch's field manager with its environment's label, CRDs
// first and the rest in Helm's install order; what the previous inventory held and the new one doesn't is pruned; and
// each object's health follows the kstatus rules
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md, "Applying"). The controller
// uses it with a client of the target: impersonating the delivery account in the installation's own cluster, or
// through a target's kubeconfig in another.
package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

var tracer = otel.Tracer("cobranch.dev/internal/adapters/cluster")

const (
	// EnvironmentLabel is the label every object Cobranch applies carries, its environment's ID as its value: what
	// Cobranch owns, and the only objects it ever replaces or deletes.
	EnvironmentLabel = "cobranch.dev/environment"
	// FieldManager is the field manager Cobranch server-side applies as.
	FieldManager = "cobranch"
)

// ErrRefused is a render the cluster won't get: an object that names another namespace, a Namespace, a
// cluster-scoped object in the installation's own cluster, an object something else owns, one the cluster serves no
// kind for, or one rendered twice. Nothing of a refused render is applied.
var ErrRefused = errors.New("refused")

// Environment is what an apply acts for: the environment's ID, which labels every object, its namespace, which every
// namespaced object takes, and whether the cluster is the installation's own, which refuses cluster-scoped objects.
type Environment struct {
	ID        string
	Namespace string
	Own       bool
}

// Ref names an object an apply applied, as an environment's inventory records it.
type Ref struct {
	Group     string `json:"group,omitempty"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	UID       string `json:"uid"`
}

// key is what makes an object the same one across renders: its group, kind, namespace and name, whatever its version.
func (r Ref) key() string {
	return r.Group + "/" + r.Kind + "/" + r.Namespace + "/" + r.Name
}

// String names the object as kubectl does, kind/name, with its namespace, for messages.
func (r Ref) String() string {
	return named(r.Kind, r.Name, r.Namespace)
}

// gvk is the object's group, version and kind.
func (r Ref) gvk() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: r.Group, Version: r.Version, Kind: r.Kind}
}

// named is kind/name, in its namespace when it has one.
func named(kind, name, namespace string) string {
	if namespace == "" {
		return kind + "/" + name
	}
	return kind + "/" + name + " in namespace " + namespace
}

// Cluster is a target cluster, reached through a client of it.
type Cluster struct {
	dynamic   dynamic.Interface
	discovery discovery.CachedDiscoveryInterface
	mapper    *restmapper.DeferredDiscoveryRESTMapper
	// Establish is how long an apply waits for the CRDs it applied to be Established, a minute by default.
	Establish time.Duration
	// poll is how often it looks.
	poll time.Duration
}

// New is the cluster cfg reaches, as cfg's credentials, or as the account cfg impersonates.
func New(cfg *rest.Config) (*Cluster, error) {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("a client of the cluster at %s: %w", cfg.Host, err)
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("discovering the cluster at %s: %w", cfg.Host, err)
	}
	cached := memory.NewMemCacheClient(disc)
	return &Cluster{dynamic: dyn, discovery: cached, mapper: restmapper.NewDeferredDiscoveryRESTMapper(cached),
		Establish: time.Minute, poll: 500 * time.Millisecond}, nil
}

// Capabilities are the cluster's Kubernetes version and the API versions it serves, each group version and each of
// its kinds as group/version/Kind, as Helm's .Capabilities names them: what a delivery renders for. A group whose
// discovery fails, such as an aggregated API that is down, is left out.
func (c *Cluster) Capabilities(ctx context.Context) (string, []string, error) {
	_, span := tracer.Start(ctx, "cluster.Capabilities")
	defer span.End()
	c.discovery.Invalidate()
	version, err := c.discovery.ServerVersion()
	if err != nil {
		return "", nil, failed(span, fmt.Errorf("reading the cluster's version: %w", err))
	}
	_, lists, err := c.discovery.ServerGroupsAndResources()
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		return "", nil, failed(span, fmt.Errorf("discovering the cluster's APIs: %w", err))
	}
	var out []string
	for _, l := range lists {
		out = append(out, l.GroupVersion)
		for _, r := range l.APIResources {
			if !strings.Contains(r.Name, "/") { // subresources aren't kinds of their own
				out = append(out, l.GroupVersion+"/"+r.Kind)
			}
		}
	}
	return version.GitVersion, out, nil
}

// failed records err on span and returns it.
func failed(span trace.Span, err error) error {
	span.SetStatus(codes.Error, err.Error())
	span.RecordError(err)
	return err
}

// refOf is the Ref of an object the cluster returned.
func refOf(u *unstructured.Unstructured) Ref {
	gvk := u.GroupVersionKind()
	return Ref{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind, Namespace: u.GetNamespace(), Name: u.GetName(),
		UID: string(u.GetUID())}
}
