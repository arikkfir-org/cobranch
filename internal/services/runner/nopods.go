package runner

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrNoPods is NoPods' answer when asked to prepare or start a run, which launch refuses before it gets there.
var ErrNoPods = errors.New("this installation runs no pods")

// NoPods is the Cluster of an installation that runs no pods (Settings.Pods off), whose controller has no access to
// the cluster and only delivers webhooks: it holds no job namespaces and no pods, so runs still running when pods went
// off fail as their pods vanish, and it starts none.
type NoPods struct{}

var _ Cluster = NoPods{}

// Prepare refuses: an installation without pods has no job namespaces.
func (NoPods) Prepare(context.Context, uuid.UUID) error { return ErrNoPods }

// Start refuses: an installation without pods starts none.
func (NoPods) Start(context.Context, Launch) error { return ErrNoPods }

// Pods reports none.
func (NoPods) Pods(context.Context, uuid.UUID) (map[uuid.UUID]Pod, error) {
	return map[uuid.UUID]Pod{}, nil
}

// Logs reports none: there is no pod to read them from.
func (NoPods) Logs(context.Context, uuid.UUID, uuid.UUID, int64) (map[string][]byte, error) {
	return map[string][]byte{}, nil
}

// Remove has nothing to remove.
func (NoPods) Remove(context.Context, uuid.UUID, uuid.UUID) error { return nil }

// Namespaces lists none.
func (NoPods) Namespaces(context.Context) ([]uuid.UUID, error) { return nil, nil }

// Drop has nothing to drop.
func (NoPods) Drop(context.Context, uuid.UUID) error { return nil }
