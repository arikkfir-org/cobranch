package runner

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/jobs"
)

// lateSlack is how long past its timeout a run's pod may stay unended before the controller ends the run itself: the
// pod's own deadline counts from when its node takes it, and one never scheduled has none.
const lateSlack = 5 * time.Minute

// Follow ends the runs whose pods ended, vanished or outlived their timeouts, keeping their logs and removing their
// pods. A run it fails to follow is followed again on the next pass; Follow goes on with the rest and answers what
// failed.
func (s *Service) Follow(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "runner.Service.Follow")
	defer span.End()
	defer func() { record(span, err) }()
	running, err := s.store.Running(ctx)
	if err != nil {
		return fmt.Errorf("listing the running runs: %w", err)
	}
	span.SetAttributes(attribute.Int("cobranch.runs.count", len(running)))
	byOrg := map[uuid.UUID][]Running{}
	for _, r := range running {
		byOrg[r.OrgID] = append(byOrg[r.OrgID], r)
	}
	var errs []error
	for orgID, runs := range byOrg {
		pods, err := s.cluster.Pods(ctx, orgID)
		if err != nil {
			errs = append(errs, fmt.Errorf("listing organization %s's pods: %w", orgID, err))
			continue
		}
		for _, r := range runs {
			pod, ok := pods[r.ID]
			if err := s.follow(ctx, r, pod, ok); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// follow ends r as its pod, which ok says it has, ended: Succeeded or Failed with the pod, Failed when the pod vanished
// or the run outlived its timeout, and leaves it running otherwise.
func (s *Service) follow(ctx context.Context, r Running, pod Pod, ok bool) error {
	ran := s.now().Sub(r.StartedAt)
	timeout := s.timeout(jobs.Job{Timeout: jobs.Duration(r.Timeout)})
	switch {
	case !ok && ran < s.settings.Vanished:
		return nil
	case !ok:
		return s.end(ctx, r.OrgID, r.RepoID, r.ID, jobs.StateFailed, "its pod vanished")
	case pod.Outcome == Succeeded:
		return s.end(ctx, r.OrgID, r.RepoID, r.ID, jobs.StateSucceeded, "succeeded")
	case pod.Outcome == Failed:
		return s.end(ctx, r.OrgID, r.RepoID, r.ID, jobs.StateFailed, pod.Reason)
	case ran > timeout+lateSlack:
		return s.end(ctx, r.OrgID, r.RepoID, r.ID, jobs.StateFailed, "timed out after "+timeout.String()+" without its pod running")
	}
	return nil
}

// end keeps the run's pod's logs, ends the run in state with reason unless it ended already, and removes its pod.
func (s *Service) end(ctx context.Context, orgID, repoID, runID uuid.UUID, state jobs.State, reason string) error {
	if err := s.keepLogs(ctx, orgID, repoID, runID); err != nil {
		return err
	}
	if _, err := s.store.Finish(ctx, runID, state, reason); err != nil {
		return fmt.Errorf("ending run %s: %w", runID, err)
	}
	if err := s.cluster.Remove(ctx, orgID, runID); err != nil {
		return fmt.Errorf("removing run %s's pod: %w", runID, err)
	}
	return nil
}

// keepLogs writes each of the run's pod's containers' logs to the archive.
func (s *Service) keepLogs(ctx context.Context, orgID, repoID, runID uuid.UUID) error {
	logs, err := s.cluster.Logs(ctx, orgID, runID, s.settings.LogLimit)
	if err != nil {
		return fmt.Errorf("reading run %s's logs: %w", runID, err)
	}
	for _, container := range sortedKeys(logs) {
		if err := s.archive.Put(ctx, repoID, runID, container, logs[container]); err != nil {
			return fmt.Errorf("keeping run %s's %s log: %w", runID, container, err)
		}
	}
	return nil
}

// Sweep cleans up after what Follow doesn't see: it removes the pods of runs that ended otherwise (a member cancelled
// them, a newer run superseded them), keeping their logs, and of runs that are gone, deletes the namespaces of
// organizations that are gone, the tokens of runs that ended or expired, and the schedules' old slots. It answers what failed, having gone on
// with the rest.
func (s *Service) Sweep(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "runner.Service.Sweep")
	defer span.End()
	defer func() { record(span, err) }()
	dropped, err := s.store.DropTokens(ctx)
	if err != nil {
		return fmt.Errorf("deleting the tokens of ended runs: %w", err)
	}
	slots, err := s.store.DropSlots(ctx, s.now().Add(-slotsKept))
	if err != nil {
		return fmt.Errorf("deleting the schedules' old slots: %w", err)
	}
	span.SetAttributes(attribute.Int64("cobranch.run_tokens.dropped", dropped), attribute.Int64("cobranch.slots.dropped", slots))
	orgs, err := s.cluster.Namespaces(ctx)
	if err != nil {
		return fmt.Errorf("listing the job namespaces: %w", err)
	}
	live, err := s.store.Organizations(ctx, orgs)
	if err != nil {
		return fmt.Errorf("finding the job namespaces' organizations: %w", err)
	}
	var errs []error
	for _, orgID := range orgs {
		if !slices.Contains(live, orgID) {
			if err := s.cluster.Drop(ctx, orgID); err != nil {
				errs = append(errs, fmt.Errorf("deleting organization %s's job namespace: %w", orgID, err))
			}
			continue
		}
		if err := s.sweep(ctx, orgID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// sweep removes the organization's pods of runs that ended or are gone, keeping the ended ones' logs. A queued run's
// pod stays: a controller is starting it.
func (s *Service) sweep(ctx context.Context, orgID uuid.UUID) error {
	pods, err := s.cluster.Pods(ctx, orgID)
	if err != nil || len(pods) == 0 {
		return wrapIf(err, "listing organization "+orgID.String()+"'s pods")
	}
	ids := make([]uuid.UUID, 0, len(pods))
	for id := range pods {
		ids = append(ids, id)
	}
	runs, err := s.store.Runs(ctx, ids)
	if err != nil {
		return fmt.Errorf("finding organization %s's runs: %w", orgID, err)
	}
	var errs []error
	for _, id := range ids {
		run, ok := runs[id]
		switch {
		case ok && run.State.Active():
			continue
		case ok:
			if err := s.keepLogs(ctx, orgID, run.RepoID, id); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if err := s.cluster.Remove(ctx, orgID, id); err != nil {
			errs = append(errs, fmt.Errorf("removing run %s's pod: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// wrapIf wraps err with what was being done, or answers nil without one.
func wrapIf(err error, what string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", what, err)
}
