package codetest

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/repos"
)

// AddRun records a run in the repository, numbered after its last.
func (s *Store) AddRun(repoID uuid.UUID, r code.Run) code.Run {
	r.Number = int64(len(s.RunList[repoID]) + 1)
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	if r.Version == 0 {
		r.Version = 1
	}
	s.RunList[repoID] = append(s.RunList[repoID], r)
	return r
}

// Runs lists the repository's runs, newest first, as code.Store does.
func (s *Store) Runs(_ context.Context, repoID uuid.UUID, sha string, before int64, limit int) ([]code.Run, error) {
	if s.Fail != nil {
		return nil, s.Fail
	}
	var out []code.Run
	for _, r := range slices.Backward(s.RunList[repoID]) {
		if (before == 0 || r.Number < before) && (sha == "" || r.SHA == sha) && len(out) < limit {
			out = append(out, r)
		}
	}
	return out, nil
}

// Run finds one of the repository's runs by number, as code.Store does.
func (s *Store) Run(_ context.Context, repoID uuid.UUID, number int64) (code.Run, error) {
	if s.Fail != nil {
		return code.Run{}, s.Fail
	}
	if number < 1 || number > int64(len(s.RunList[repoID])) {
		return code.Run{}, repos.ErrNotFound
	}
	return s.RunList[repoID][number-1], nil
}

// CancelRun cancels a run that hasn't ended, as code.Store does, naming actor by Names.
func (s *Store) CancelRun(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, number int64) (code.Run, error) {
	return s.change(ctx, repoID, number, func(r *code.Run) error {
		if !r.State.Active() {
			return code.ErrRunEnded
		}
		r.State, r.Reason = jobs.StateCancelled, "cancelled by "+s.Names[actor]
		now := s.Now
		r.EndedAt = &now
		return nil
	})
}

// StartRun queues a waiting run for actor, as code.Store does.
func (s *Store) StartRun(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, number int64) (code.Run, error) {
	return s.change(ctx, repoID, number, func(r *code.Run) error {
		if r.State != jobs.StateWaiting {
			return code.ErrRunNotWaiting
		}
		r.State, r.Reason = jobs.StateQueued, ""
		r.StartedBy = &code.Principal{ID: actor, DisplayName: s.Names[actor]}
		return nil
	})
}

// change applies fn to a run and bumps its version, unless fn refuses.
func (s *Store) change(ctx context.Context, repoID uuid.UUID, number int64, fn func(r *code.Run) error) (code.Run, error) {
	r, err := s.Run(ctx, repoID, number)
	if err != nil {
		return code.Run{}, err
	}
	if err := fn(&r); err != nil {
		return code.Run{}, err
	}
	r.Version++
	s.RunList[repoID][number-1] = r
	return r, nil
}
