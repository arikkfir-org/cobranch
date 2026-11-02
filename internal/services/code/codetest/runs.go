package codetest

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/jobs"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// AddRun records a run in the repository, numbered after its last.
func (s *Store) AddRun(repoID uuid.UUID, r code.Run) code.Run {
	r.Number, r.RepoID = int64(len(s.RunList[repoID])+1), repoID
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

// RerunRun queues an ended run again as its next attempt, for actor, as code.Store does.
func (s *Store) RerunRun(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, number int64) (code.Run, error) {
	old, err := s.Run(ctx, repoID, number)
	if err != nil {
		return code.Run{}, err
	}
	if s.activeAt(repoID, old.Job.DisplayName, old.SHA) {
		return code.Run{}, code.ErrRunActive
	}
	first := old.Number
	if old.RerunOf != 0 {
		first = old.RerunOf
	}
	attempt := int32(1)
	for _, r := range s.RunList[repoID] {
		if r.Number == first || r.RerunOf == first {
			attempt = max(attempt, max(r.Attempt, 1)+1)
		}
	}
	return s.AddRun(repoID, code.Run{Job: old.Job, SHA: old.SHA, Ref: old.Ref, Trigger: old.Trigger, Definition: old.Definition,
		State: jobs.StateQueued, Attempt: attempt, RerunOf: first, StartedBy: &code.Principal{ID: actor, DisplayName: s.Names[actor]},
		QueuedAt: s.Now}), nil
}

// RunJob queues a run of a manual job at its branch's head, as code.Store does, from Defined.
func (s *Store) RunJob(_ context.Context, actor uuid.UUID, repoID uuid.UUID, branch, job string) (code.Run, error) {
	head, defined, ok := s.defined(repoID, branch)
	i := slices.IndexFunc(defined, func(j jobs.Job) bool { return j.Name == job })
	switch {
	case !ok || i < 0:
		return code.Run{}, repos.ErrNotFound
	case defined[i].On.Manual == nil:
		return code.Run{}, code.ErrNotManual
	case s.activeAt(repoID, job, head):
		return code.Run{}, code.ErrRunActive
	}
	e := jobs.Event{Kind: jobs.EventManual, RepoID: repoID, SHA: head, Ref: "refs/heads/" + branch, Name: job, ActorID: actor}
	return s.AddRun(repoID, code.Run{Job: code.Principal{DisplayName: job}, SHA: head, Ref: e.Ref, Trigger: e, Definition: defined[i],
		State: jobs.StateQueued, Attempt: 1, QueuedAt: s.Now}), nil
}

// PostEvent queues the runs of the default branch's jobs that follow the event, as code.Store does, from Defined.
func (s *Store) PostEvent(_ context.Context, actor uuid.UUID, repoID uuid.UUID, in code.EventInput) ([]code.Run, error) {
	head, _, ok := s.defined(repoID, in.Branch)
	if !ok {
		return nil, &orgs.ValidationError{Field: "branch", Code: "branch.unknown", Message: "the repository has no live branch of this name"}
	}
	_, defined, _ := s.defined(repoID, "main")
	e := jobs.Event{Kind: jobs.EventPosted, RepoID: repoID, SHA: head, Ref: "refs/heads/" + in.Branch, Name: in.Name, ActorID: actor, Data: in.Data}
	var out []code.Run
	for _, j := range defined {
		if j.Matches(e) && !s.activeAt(repoID, j.Name, head) {
			out = append(out, s.AddRun(repoID, code.Run{Job: code.Principal{DisplayName: j.Name}, SHA: head, Ref: e.Ref, Trigger: e,
				Definition: j, State: jobs.StateQueued, Attempt: 1, QueuedAt: s.Now}))
		}
	}
	return out, nil
}

// defined finds a live branch's head and the jobs Defined there.
func (s *Store) defined(repoID uuid.UUID, branch string) (string, []jobs.Job, bool) {
	for _, b := range s.BranchList[repoID] {
		if b.Name == branch {
			return b.Head, s.Defined[repoID][branch], true
		}
	}
	return "", nil, false
}

// activeAt reports whether a run of the job at the commit hasn't ended.
func (s *Store) activeAt(repoID uuid.UUID, job, sha string) bool {
	return slices.ContainsFunc(s.RunList[repoID], func(r code.Run) bool { return r.Job.DisplayName == job && r.SHA == sha && r.State.Active() })
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
