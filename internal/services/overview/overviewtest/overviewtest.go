// Package overviewtest fakes the overview domain's port for tests of what uses its service.
package overviewtest

import (
	"context"
	"slices"
	"sync"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/overview"
)

// Store is an in-memory overview.Store: each organization's overview as a store reads it, of which it answers the
// repositories asked for, and the last period it was asked.
type Store struct {
	mu sync.Mutex
	// Overviews are the organizations' overviews, by ID; ByRepository holds every repository's share, whoever reads it.
	Overviews map[uuid.UUID]overview.Overview
	// Asked is the last period Overview was asked for.
	Asked overview.Period
	// Fail, when set, is what every call answers.
	Fail error
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{Overviews: map[uuid.UUID]overview.Overview{}}
}

// Overview answers the organization's overview, with only the repositories of repoIDs among ByRepository.
func (s *Store) Overview(_ context.Context, orgID uuid.UUID, repoIDs []uuid.UUID, p overview.Period) (overview.Overview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Asked = p
	if s.Fail != nil {
		return overview.Overview{}, s.Fail
	}
	o := s.Overviews[orgID]
	o.ByRepository = slices.DeleteFunc(slices.Clone(o.ByRepository), func(r overview.RepositoryFigures) bool { return !slices.Contains(repoIDs, r.RepoID) })
	if o.Issues != nil {
		issues := *o.Issues
		o.Issues = &issues
	}
	return o, nil
}
