package deliverer

import (
	"context"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/runner"
)

// NoPods is the Pods of an installation that runs no pods (Settings.Pods off), whose controller has no access to the
// cluster: it starts no render pod and reports none, so Dispatch fails each delivery, saying why, before it gets here.
type NoPods struct{}

var _ Pods = NoPods{}

// Prepare refuses: an installation without pods has no job namespaces.
func (NoPods) Prepare(context.Context, uuid.UUID) error { return runner.ErrNoPods }

// StartRender refuses: an installation without pods starts none.
func (NoPods) StartRender(context.Context, Render) error { return runner.ErrNoPods }

// Renders reports none.
func (NoPods) Renders(context.Context, uuid.UUID) (map[uuid.UUID]runner.Pod, error) {
	return map[uuid.UUID]runner.Pod{}, nil
}

// RenderLogs reports none: there is no pod to read them from.
func (NoPods) RenderLogs(context.Context, uuid.UUID, uuid.UUID, int64) (map[string][]byte, error) {
	return map[string][]byte{}, nil
}

// RemoveRender has nothing to remove.
func (NoPods) RemoveRender(context.Context, uuid.UUID, uuid.UUID) error { return nil }

// Namespaces lists none.
func (NoPods) Namespaces(context.Context) ([]uuid.UUID, error) { return nil, nil }
