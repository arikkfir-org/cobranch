// Package livetest holds a fake of live.Notifier, for the tests of the services that follow reads live and of the
// adapters that serve them.
package livetest

import (
	"slices"
	"sync"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/live"
)

// Notifier is a live.Notifier that the test wakes.
type Notifier struct {
	mu       sync.Mutex
	watchers map[uuid.UUID][]chan struct{}
}

var _ live.Notifier = (*Notifier)(nil)

// Watch returns a channel that Wake wakes for any of ids.
func (n *Notifier) Watch(ids ...uuid.UUID) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.watchers == nil {
		n.watchers = map[uuid.UUID][]chan struct{}{}
	}
	for _, id := range ids {
		n.watchers[id] = append(n.watchers[id], ch)
	}
	return ch, func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		for _, id := range ids {
			n.watchers[id] = slices.DeleteFunc(n.watchers[id], func(c chan struct{}) bool { return c == ch })
		}
	}
}

// Wake wakes those who watch id.
func (n *Notifier) Wake(id uuid.UUID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, ch := range n.watchers[id] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Watching counts those who watch id.
func (n *Notifier) Watching(id uuid.UUID) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.watchers[id])
}
