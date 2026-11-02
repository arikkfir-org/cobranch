package cluster

import (
	"context"
	"fmt"
	"slices"

	"github.com/fluxcd/cli-utils/pkg/kstatus/status"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Health is an object's or an environment's health, as the Console shows it.
type Health string

// The healths, from best to worst: an environment's is its objects' worst.
const (
	Healthy     Health = "Healthy"
	Progressing Health = "Progressing"
	Unknown     Health = "Unknown"
	Missing     Health = "Missing"
	Degraded    Health = "Degraded"
)

// healths ranks the healths, best first.
var healths = []Health{Healthy, Progressing, Unknown, Missing, Degraded}

// Worst is the worst of hs, Healthy when there are none: an environment's health.
func Worst(hs ...Health) Health {
	worst := Healthy
	for _, h := range hs {
		if slices.Index(healths, h) > slices.Index(healths, worst) {
			worst = h
		}
	}
	return worst
}

// ObjectHealth is one inventoried object's health, with kstatus's message saying why.
type ObjectHealth struct {
	Ref     Ref
	Health  Health
	Message string
}

// Health reads each of refs from the cluster and gives its health by the kstatus rules, which Kubernetes' own tools
// share: Current is Healthy, InProgress and Terminating Progressing, Failed Degraded, an object gone Missing, and one
// it can't read Unknown, with why. It returns the objects' healths in refs' order, and the worst of them.
func (c *Cluster) Health(ctx context.Context, refs []Ref) ([]ObjectHealth, Health, error) {
	ctx, span := tracer.Start(ctx, "cluster.Health", trace.WithAttributes(attribute.Int("cobranch.delivery.objects", len(refs))))
	defer span.End()
	out := make([]ObjectHealth, 0, len(refs))
	all := make([]Health, 0, len(refs))
	for _, r := range refs {
		h := c.health(ctx, r)
		out = append(out, h)
		all = append(all, h.Health)
	}
	if err := ctx.Err(); err != nil {
		return nil, "", failed(span, fmt.Errorf("reading the objects' health: %w", err))
	}
	return out, Worst(all...), nil
}

// health reads one object's health.
func (c *Cluster) health(ctx context.Context, r Ref) ObjectHealth {
	m, err := c.mapping(r)
	if meta.IsNoMatchError(err) {
		return ObjectHealth{Ref: r, Health: Missing, Message: "the cluster no longer serves its kind"}
	} else if err != nil {
		return ObjectHealth{Ref: r, Health: Unknown, Message: err.Error()}
	}
	u, err := c.client(m.Resource, m.Scope.Name() == meta.RESTScopeNameNamespace, r.Namespace).Get(ctx, r.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return ObjectHealth{Ref: r, Health: Missing, Message: "it is gone"}
	} else if err != nil {
		return ObjectHealth{Ref: r, Health: Unknown, Message: "reading it: " + err.Error()}
	}
	h, message := healthOf(u)
	return ObjectHealth{Ref: r, Health: h, Message: message}
}

// healthOf is u's health by the kstatus rules, and its message.
func healthOf(u *unstructured.Unstructured) (Health, string) {
	result, err := status.Compute(u)
	if err != nil {
		return Unknown, "computing its status: " + err.Error()
	}
	switch result.Status {
	case status.CurrentStatus:
		return Healthy, result.Message
	case status.InProgressStatus, status.TerminatingStatus:
		return Progressing, result.Message
	case status.FailedStatus:
		return Degraded, result.Message
	case status.NotFoundStatus:
		return Missing, result.Message
	default:
		return Unknown, result.Message
	}
}
