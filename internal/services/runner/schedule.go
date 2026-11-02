package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// ScheduleLate is how late a schedule's slot may still queue its run: a controller that was down longer skips the
// slots it missed rather than run a burst of them.
const ScheduleLate = 10 * time.Minute

// slotsKept is how long the record of a queued slot is kept: far longer than ScheduleLate, past which no controller
// queues it again.
const slotsKept = 24 * time.Hour

// Schedule queues a run for each schedule slot that fell due within ScheduleLate, at its repository's default branch's
// head, each slot once whichever replica gets to it first
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "Triggers"). A slot it fails to
// queue is tried again on the next pass while it is due; Schedule goes on with the rest and answers what failed.
func (s *Service) Schedule(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "runner.Service.Schedule")
	defer span.End()
	defer func() { record(span, err) }()
	scheduled, err := s.store.Scheduled(ctx)
	if err != nil {
		return fmt.Errorf("listing the scheduled jobs: %w", err)
	}
	now := s.now()
	queued := 0
	var errs []error
	for _, r := range scheduled {
		for _, job := range r.Jobs {
			slot, due := job.DueSlot(now, ScheduleLate)
			if !due {
				continue
			}
			ok, err := s.store.QueueSlot(ctx, r.RepoID, job.Name, slot)
			if err != nil {
				errs = append(errs, fmt.Errorf("queueing repository %s's job %s at %s: %w", r.RepoID, job.Name, slot.Format(time.RFC3339), err))
				continue
			}
			if ok {
				queued++
			}
		}
	}
	span.SetAttributes(attribute.Int("cobranch.repositories.count", len(scheduled)), attribute.Int("cobranch.slots.queued", queued))
	return errors.Join(errs...)
}
