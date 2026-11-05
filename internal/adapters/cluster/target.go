package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/deliverer"
)

// Target is what the deliverer's clusters share of deliverer.Target, on one Cluster: reading what it serves, applying,
// pruning, reading health and tearing down. The in-cluster and remote adapters embed it, and add how each prepares an
// environment's namespace and releases it.
type Target struct {
	Cluster *Cluster
	// Own is whether the cluster is the installation's own, which refuses cluster-scoped objects.
	Own bool
	// Guard refuses a render before any of it applies, with deliverer.ErrRefused saying why; nil refuses nothing.
	Guard func(rendered []*unstructured.Unstructured) error
}

// Capabilities are the cluster's Kubernetes version and the API versions it serves (deliverer.Target).
func (t *Target) Capabilities(ctx context.Context) (string, []string, error) {
	version, apis, err := t.Cluster.Capabilities(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("discovering the cluster: %w", err)
	}
	return version, apis, nil
}

// Apply applies manifests to env's namespace, refusing all of it when Guard does or when it holds an object the
// namespace can't take (deliverer.Target).
func (t *Target) Apply(ctx context.Context, env deliverer.Environment, manifests []byte) ([]deliverer.Object, error) {
	rendered, err := Decode(manifests)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", deliverer.ErrRefused, err)
	}
	if t.Guard != nil {
		if err := t.Guard(rendered); err != nil {
			return nil, err
		}
	}
	refs, err := t.Cluster.Apply(ctx, t.environment(env), rendered)
	objects := objectsOf(refs)
	if errors.Is(err, ErrRefused) {
		return objects, fmt.Errorf("%w: %s", deliverer.ErrRefused, strings.TrimPrefix(err.Error(), ErrRefused.Error()+": "))
	}
	if err != nil {
		return objects, fmt.Errorf("applying to namespace %s: %w", env.Namespace, err)
	}
	return objects, nil
}

// Prune deletes what previous holds and current doesn't (deliverer.Target).
func (t *Target) Prune(ctx context.Context, env deliverer.Environment, previous, current []deliverer.Object) error {
	if err := t.Cluster.Prune(ctx, t.environment(env), refsOf(previous), refsOf(current)); err != nil {
		return fmt.Errorf("pruning namespace %s: %w", env.Namespace, err)
	}
	return nil
}

// Health reads the objects' healths, in order, and the worst of them (deliverer.Target).
func (t *Target) Health(ctx context.Context, objects []deliverer.Object) ([]deliverer.Object, code.Health, error) {
	read, worst, err := t.Cluster.Health(ctx, refsOf(objects))
	if err != nil {
		return nil, "", fmt.Errorf("reading health: %w", err)
	}
	out := make([]deliverer.Object, 0, len(read))
	for _, h := range read {
		o := objectOf(h.Ref)
		o.Health, o.Message, o.Ready, o.Created = consoleHealth(h.Health), h.Message, h.Ready, h.Created
		out = append(out, o)
	}
	return out, consoleHealth(worst), nil
}

// Teardown deletes those of objects that are still env's, and answers which remain (deliverer.Target).
func (t *Target) Teardown(ctx context.Context, env deliverer.Environment, objects []deliverer.Object) ([]deliverer.Object, error) {
	remaining, err := t.Cluster.Teardown(ctx, t.environment(env), refsOf(objects))
	left := make([]deliverer.Object, 0, len(remaining))
	for _, r := range remaining {
		left = append(left, objectOf(r))
	}
	if err != nil {
		return left, fmt.Errorf("tearing namespace %s down: %w", env.Namespace, err)
	}
	return left, nil
}

// environment is env as the cluster applies for it.
func (t *Target) environment(env deliverer.Environment) Environment {
	return Environment{ID: env.ID.String(), Namespace: env.Namespace, Own: t.Own}
}

// objectOf is the inventory's object of r, its health not read.
func objectOf(r Ref) deliverer.Object {
	return deliverer.Object{Group: r.Group, Version: r.Version, Kind: r.Kind, Namespace: r.Namespace, Name: r.Name, UID: r.UID,
		Health: code.Unknown}
}

// objectsOf is the inventory's objects of refs, in order.
func objectsOf(refs []Ref) []deliverer.Object {
	out := make([]deliverer.Object, 0, len(refs))
	for _, r := range refs {
		out = append(out, objectOf(r))
	}
	return out
}

// refsOf is the cluster's refs of objects, in order.
func refsOf(objects []deliverer.Object) []Ref {
	out := make([]Ref, 0, len(objects))
	for _, o := range objects {
		out = append(out, Ref{Group: o.Group, Version: o.Version, Kind: o.Kind, Namespace: o.Namespace, Name: o.Name, UID: o.UID})
	}
	return out
}

// consoleHealth is h as the Console's environments say it.
func consoleHealth(h Health) code.Health {
	return code.Health(strings.ToLower(string(h)))
}
