package taskrunner

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/runner"
)

// Follow keeps the running tasks' pods: it renews each task's token every RenewEvery, fails a task that outlived its
// timeout, and starts a task's pod again when it died, vanished or ended while the task hadn't, failing the task once
// it has had MaxPods. A task it fails to follow is followed again on the next pass; Follow goes on with the rest and
// answers what failed.
func (s *Service) Follow(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "taskrunner.Service.Follow")
	defer span.End()
	defer func() { record(span, err) }()
	active, err := s.store.Active(ctx)
	if err != nil {
		return fmt.Errorf("listing the running tasks: %w", err)
	}
	span.SetAttributes(attribute.Int("cobranch.tasks.count", len(active)))
	byOrg := map[uuid.UUID][]Active{}
	for _, a := range active {
		byOrg[a.OrgID] = append(byOrg[a.OrgID], a)
	}
	var errs []error
	for orgID, tasks := range byOrg {
		pods, err := s.cluster.TaskPods(ctx, orgID)
		if err != nil {
			errs = append(errs, fmt.Errorf("listing organization %s's task pods: %w", orgID, err))
			continue
		}
		for _, a := range tasks {
			if err := s.follow(ctx, a, pods[a.ID]); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// follow keeps a's pod, which pod reports when it is a's newest: it fails a that outlived its timeout, starts its pod
// again when it died, and renews its token otherwise.
func (s *Service) follow(ctx context.Context, a Active, pod Pod) error {
	now := s.now()
	ok := pod.Number == a.Pod
	switch {
	case now.Sub(a.StartedAt) > s.settings.Timeout:
		return s.end(ctx, a, "timed out after "+s.settings.Timeout.String())
	case !ok && now.Sub(a.PodStartedAt) < s.settings.Vanished:
		return nil
	case !ok:
		return s.restart(ctx, a, "its pod vanished")
	case pod.Outcome == runner.Failed:
		return s.restart(ctx, a, pod.Reason)
	case pod.Outcome == runner.Succeeded:
		return s.restart(ctx, a, "its harness exited while the task hadn't ended")
	case now.Sub(a.TokenAt) >= RenewEvery:
		return s.renew(ctx, a)
	}
	return nil
}

// restart starts a's next pod in place of the one that died for reason, or fails a once it has had MaxPods. A task that
// ended meanwhile leaves its pods to the sweep.
func (s *Service) restart(ctx context.Context, a Active, reason string) error {
	if a.Pod >= MaxPods {
		return s.end(ctx, a, fmt.Sprintf("its pod died %d times; the last time, %s", a.Pod, reason))
	}
	t, err := s.store.Task(ctx, a.ID)
	switch {
	case errors.Is(err, orgs.ErrNotFound):
		return nil
	case err != nil:
		return fmt.Errorf("reading task %s to start its pod again: %w", a.ID, err)
	}
	if err := s.cluster.RemoveTask(ctx, a.OrgID, a.ID); err != nil {
		return fmt.Errorf("removing task %s's pod %d: %w", a.ID, a.Pod, err)
	}
	_, err = s.startPod(ctx, t, a.Pod+1)
	return err
}

// renew issues a a new token and writes it into its pod's Secret; the one it holds stays valid until it expires.
func (s *Service) renew(ctx context.Context, a Active) error {
	token, err := s.token(ctx, a.ID)
	if err != nil {
		return err
	}
	if err := s.cluster.Renew(ctx, a.OrgID, a.ID, a.Pod, token); err != nil {
		return fmt.Errorf("renewing task %s's token: %w", a.ID, err)
	}
	return nil
}

// end fails a for reason, unless it ended already, and removes its pods.
func (s *Service) end(ctx context.Context, a Active, reason string) error {
	if _, err := s.store.Fail(ctx, a.ID, reason); err != nil {
		return fmt.Errorf("failing task %s: %w", a.ID, err)
	}
	if err := s.cluster.RemoveTask(ctx, a.OrgID, a.ID); err != nil {
		return fmt.Errorf("removing task %s's pods: %w", a.ID, err)
	}
	return nil
}

// Sweep removes the pods of tasks that ended, or are gone: a task its agent finished, or someone cancelled, leaves its
// pod to the sweep, though its harness exits once Cobranch refuses its token. It answers what failed, having gone on
// with the rest.
func (s *Service) Sweep(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "taskrunner.Service.Sweep")
	defer span.End()
	defer func() { record(span, err) }()
	orgs, err := s.cluster.Namespaces(ctx)
	if err != nil {
		return fmt.Errorf("listing the job namespaces: %w", err)
	}
	var errs []error
	for _, orgID := range orgs {
		if err := s.sweep(ctx, orgID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// sweep removes the organization's pods of tasks that ended or are gone. A queued task's pod stays: a controller is
// starting it.
func (s *Service) sweep(ctx context.Context, orgID uuid.UUID) error {
	pods, err := s.cluster.TaskPods(ctx, orgID)
	if err != nil || len(pods) == 0 {
		return wrapIf(err, "listing organization "+orgID.String()+"'s task pods")
	}
	ids := make([]uuid.UUID, 0, len(pods))
	for id := range pods {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	states, err := s.store.States(ctx, ids)
	if err != nil {
		return fmt.Errorf("finding organization %s's tasks: %w", orgID, err)
	}
	var errs []error
	for _, id := range ids {
		if state, ok := states[id]; ok && state.Active() {
			continue
		}
		if err := s.cluster.RemoveTask(ctx, orgID, id); err != nil {
			errs = append(errs, fmt.Errorf("removing task %s's pods: %w", id, err))
		}
	}
	return errors.Join(errs...)
}
