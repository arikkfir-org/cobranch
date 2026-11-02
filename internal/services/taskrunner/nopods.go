package taskrunner

import (
	"context"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/runner"
)

// NoPods is the Cluster of an installation that runs no pods (Settings.Pods off), whose controller has no access to
// the cluster, as runner.NoPods is for runs: it holds no job namespaces and no pods, so a task still running when pods
// went off fails once its pod counts as vanished, and it starts none.
type NoPods struct{}

var _ Cluster = NoPods{}

// Prepare refuses: an installation without pods has no job namespaces.
func (NoPods) Prepare(context.Context, uuid.UUID) error { return runner.ErrNoPods }

// StartTask refuses: an installation without pods starts none.
func (NoPods) StartTask(context.Context, Launch) error { return runner.ErrNoPods }

// TaskPods reports none.
func (NoPods) TaskPods(context.Context, uuid.UUID) (map[uuid.UUID]Pod, error) {
	return map[uuid.UUID]Pod{}, nil
}

// Renew refuses: there is no pod to renew the token of.
func (NoPods) Renew(context.Context, uuid.UUID, uuid.UUID, int, string) error {
	return runner.ErrNoPods
}

// RemoveTask has nothing to remove.
func (NoPods) RemoveTask(context.Context, uuid.UUID, uuid.UUID) error { return nil }

// Namespaces lists none.
func (NoPods) Namespaces(context.Context) ([]uuid.UUID, error) { return nil, nil }
