package codetest

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/repos"
)

// AddEnvironment records an environment in the repository.
func (s *Store) AddEnvironment(repoID uuid.UUID, e code.Environment) code.Environment {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	if e.Version == 0 {
		e.Version = 1
	}
	s.EnvironmentList[repoID] = append(s.EnvironmentList[repoID], e)
	return e
}

// AddDelivery records a delivery of one of the repository's environments, numbered after its last.
func (s *Store) AddDelivery(repoID uuid.UUID, d code.Delivery) code.Delivery {
	d.Number = int64(len(s.DeliveryList[repoID]) + 1)
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	if d.Version == 0 {
		d.Version = 1
	}
	if i := slices.IndexFunc(s.EnvironmentList[repoID], func(e code.Environment) bool { return e.ID == d.EnvironmentID }); i >= 0 {
		d.Target, d.Branch = s.EnvironmentList[repoID][i].Target.DisplayName, s.EnvironmentList[repoID][i].Branch
	}
	s.DeliveryList[repoID] = append(s.DeliveryList[repoID], d)
	return d
}

// Environments lists the repository's environments by target and branch, of branch unless it is empty, as code.Store
// does.
func (s *Store) Environments(_ context.Context, repoID uuid.UUID, branch string) ([]code.Environment, error) {
	if s.Fail != nil {
		return nil, s.Fail
	}
	var out []code.Environment
	for _, e := range s.EnvironmentList[repoID] {
		if branch == "" || e.Branch == branch {
			out = append(out, s.withLatest(repoID, e))
		}
	}
	slices.SortFunc(out, func(a, b code.Environment) int {
		if c := strings.Compare(a.Target.DisplayName, b.Target.DisplayName); c != 0 {
			return c
		}
		return strings.Compare(a.Branch, b.Branch)
	})
	return out, nil
}

// Environment finds one of the repository's environments, as code.Store does.
func (s *Store) Environment(_ context.Context, repoID, id uuid.UUID) (code.Environment, error) {
	if s.Fail != nil {
		return code.Environment{}, s.Fail
	}
	for _, e := range s.EnvironmentList[repoID] {
		if e.ID == id {
			return s.withLatest(repoID, e), nil
		}
	}
	return code.Environment{}, repos.ErrNotFound
}

// withLatest is e with its newest delivery.
func (s *Store) withLatest(repoID uuid.UUID, e code.Environment) code.Environment {
	for _, d := range slices.Backward(s.DeliveryList[repoID]) {
		if d.EnvironmentID == e.ID {
			e.Latest = &d
			break
		}
	}
	return e
}

// Deliveries lists an environment's deliveries, newest first, as code.Store does.
func (s *Store) Deliveries(ctx context.Context, repoID, environmentID uuid.UUID, before int64, limit int) ([]code.Delivery, error) {
	if _, err := s.Environment(ctx, repoID, environmentID); err != nil {
		return nil, err
	}
	var out []code.Delivery
	for _, d := range slices.Backward(s.DeliveryList[repoID]) {
		if d.EnvironmentID == environmentID && (before == 0 || d.Number < before) && len(out) < limit {
			out = append(out, d)
		}
	}
	return out, nil
}

// Delivery finds one of the repository's deliveries by number, as code.Store does.
func (s *Store) Delivery(_ context.Context, repoID uuid.UUID, number int64) (code.Delivery, error) {
	if s.Fail != nil {
		return code.Delivery{}, s.Fail
	}
	if number < 1 || number > int64(len(s.DeliveryList[repoID])) {
		return code.Delivery{}, repos.ErrNotFound
	}
	return s.DeliveryList[repoID][number-1], nil
}

// Redeliver queues a delivery of the environment's branch's head for actor, superseding the others that haven't
// started applying, as code.Store does.
func (s *Store) Redeliver(ctx context.Context, actor uuid.UUID, repoID, environmentID uuid.UUID) (code.Delivery, error) {
	e, err := s.Environment(ctx, repoID, environmentID)
	if err != nil {
		return code.Delivery{}, err
	}
	if e.Paused {
		return code.Delivery{}, code.ErrEnvironmentPaused
	}
	return s.deliverHead(repoID, e, actor, code.TriggeredByRedeliver)
}

// PauseEnvironment pauses an environment for actor, cancelling its deliveries that haven't started applying, as
// code.Store does.
func (s *Store) PauseEnvironment(ctx context.Context, actor uuid.UUID, repoID, environmentID uuid.UUID) (code.Environment, error) {
	return s.changeEnvironment(ctx, repoID, environmentID, func(e *code.Environment) error {
		if e.Paused {
			return code.ErrEnvironmentPaused
		}
		e.Paused = true
		s.cancelDeliveries(repoID, e.ID, "paused by "+s.Names[actor])
		return nil
	})
}

// ResumeEnvironment resumes a paused environment for actor and queues a delivery of its branch's head, as code.Store
// does.
func (s *Store) ResumeEnvironment(ctx context.Context, actor uuid.UUID, repoID, environmentID uuid.UUID) (code.Environment, error) {
	e, err := s.changeEnvironment(ctx, repoID, environmentID, func(e *code.Environment) error {
		if !e.Paused {
			return code.ErrEnvironmentNotPaused
		}
		e.Paused = false
		return nil
	})
	if err != nil {
		return code.Environment{}, err
	}
	if _, err := s.deliverHead(repoID, e, actor, code.TriggeredByResume); err != nil {
		return code.Environment{}, err
	}
	return s.Environment(ctx, repoID, environmentID)
}

// StartDelivery queues a waiting delivery for actor, as code.Store does.
func (s *Store) StartDelivery(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, number int64) (code.Delivery, error) {
	d, err := s.Delivery(ctx, repoID, number)
	if err != nil {
		return code.Delivery{}, err
	}
	if d.State != code.DeliveryWaiting {
		return code.Delivery{}, code.ErrDeliveryNotWaiting
	}
	d.State, d.Reason, d.Version = code.DeliveryQueued, "", d.Version+1
	d.StartedBy = &code.Principal{ID: actor, DisplayName: s.Names[actor]}
	s.DeliveryList[repoID][number-1] = d
	return d, nil
}

// KeepRender keeps a rendering delivery's render, once, as code.Store does.
func (s *Store) KeepRender(_ context.Context, deliveryID uuid.UUID, manifests []byte) error {
	for _, list := range s.DeliveryList {
		for _, d := range list {
			if d.ID != deliveryID {
				continue
			}
			if _, kept := s.Renders[deliveryID]; kept || d.State != code.DeliveryRendering {
				return code.ErrDeliveryNotRendering
			}
			s.Renders[deliveryID] = manifests
			return nil
		}
	}
	return code.ErrDeliveryNotRendering
}

// deliverHead queues a delivery of the head of e's branch for actor, superseding e's others.
func (s *Store) deliverHead(repoID uuid.UUID, e code.Environment, actor uuid.UUID, trigger code.DeliveryTrigger) (code.Delivery, error) {
	i := slices.IndexFunc(s.BranchList[repoID], func(b code.Branch) bool { return b.Name == e.Branch })
	if i < 0 {
		return code.Delivery{}, repos.ErrNotFound
	}
	number := int64(len(s.DeliveryList[repoID]) + 1)
	s.cancelDeliveries(repoID, e.ID, "superseded by #"+strconv.FormatInt(number, 10))
	return s.AddDelivery(repoID, code.Delivery{EnvironmentID: e.ID, Kind: code.DeliverCommit, SHA: s.BranchList[repoID][i].Head,
		Trigger: trigger, State: code.DeliveryQueued, StartedBy: &code.Principal{ID: actor, DisplayName: s.Names[actor]},
		QueuedAt: s.Now}), nil
}

// cancelDeliveries cancels an environment's deliveries that haven't started applying, for reason.
func (s *Store) cancelDeliveries(repoID, environmentID uuid.UUID, reason string) {
	for i, d := range s.DeliveryList[repoID] {
		if d.EnvironmentID == environmentID && (d.State == code.DeliveryWaiting || d.State == code.DeliveryQueued || d.State == code.DeliveryRendering) {
			now := s.Now
			d.State, d.Reason, d.EndedAt, d.Version = code.DeliveryCancelled, reason, &now, d.Version+1
			s.DeliveryList[repoID][i] = d
		}
	}
}

// changeEnvironment applies fn to an environment and bumps its version, unless fn refuses.
func (s *Store) changeEnvironment(ctx context.Context, repoID, id uuid.UUID, fn func(e *code.Environment) error) (code.Environment, error) {
	if _, err := s.Environment(ctx, repoID, id); err != nil {
		return code.Environment{}, err
	}
	i := slices.IndexFunc(s.EnvironmentList[repoID], func(e code.Environment) bool { return e.ID == id })
	e := s.EnvironmentList[repoID][i]
	if err := fn(&e); err != nil {
		return code.Environment{}, err
	}
	e.Version++
	s.EnvironmentList[repoID][i] = e
	return s.withLatest(repoID, e), nil
}
