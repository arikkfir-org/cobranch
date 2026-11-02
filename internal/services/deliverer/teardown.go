package deliverer

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/code"
)

// remainingShown is how many of what a teardown left a reason names.
const remainingShown = 5

// startTeardown marks a claimed teardown deleting and deletes for it at once; Follow looks at it again until what its
// environment held is gone (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md,
// "Teardown").
func (s *Service) startTeardown(ctx context.Context, d Delivery) error {
	marked, err := s.store.MarkDeleting(ctx, d.ID)
	if err != nil || !marked {
		return wrapIf(err, "marking teardown "+d.ID.String()+" deleting")
	}
	return s.tearDown(ctx, d.ID)
}

// tearDown takes a teardown that deletes, unless another controller holds it, and deletes what its environment still
// holds: the objects first, then its namespace if Cobranch made it. It ends Succeeded once all is gone, and Failed once
// TeardownTimeout passed with something left, or when the environment's cluster can't be opened; until then the
// controller looks again after HealthEvery.
func (s *Service) tearDown(ctx context.Context, deliveryID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "deliverer.Service.tearDown")
	defer span.End()
	defer func() { record(span, err) }()
	t, ok, err := s.store.TakeTeardown(ctx, deliveryID, s.now().Add(s.settings.Lease))
	if err != nil || !ok {
		return wrapIf(err, "taking teardown "+deliveryID.String())
	}
	span.SetAttributes(deliveryAttrs(t.ID, t.OrgID, t.RepoID, t.Number)...)
	span.SetAttributes(attribute.String("cobranch.environment.id", t.Environment.ID.String()))
	target, why, err := s.open(ctx, t.Environment)
	switch {
	case err != nil:
		return err
	case why == noCluster && len(t.Inventory) == 0:
		// Nothing was ever applied where this installation no longer reaches: there is nothing to delete.
		return s.endTeardown(ctx, t, nil, code.DeliverySucceeded, "")
	case why != "":
		return s.endTeardown(ctx, t, t.Inventory, code.DeliveryFailed, why)
	}
	remaining, err := target.Teardown(ctx, t.Environment, t.Inventory)
	if len(remaining) > 0 {
		return s.lookAgain(ctx, t, remaining, wrapIf(err, "deleting environment "+t.Environment.ID.String()+"'s objects"))
	}
	// A failure that left nothing behind changes nothing: what it failed to delete is gone all the same.
	gone, err := target.Release(ctx, t.Environment)
	switch {
	case err != nil:
		return s.lookAgain(ctx, t, nil, fmt.Errorf("deleting environment %s's namespace: %w", t.Environment.ID, err))
	case !gone:
		return s.lookAgain(ctx, t, nil, nil)
	}
	return s.endTeardown(ctx, t, nil, code.DeliverySucceeded, "")
}

// lookAgain fails t once TeardownTimeout passed, naming what remains, or the namespace when nothing else does, and
// otherwise holds it until the controller looks again; failed is why this pass didn't finish, if it failed.
func (s *Service) lookAgain(ctx context.Context, t Teardown, remaining []Object, failed error) error {
	if s.now().Sub(t.StartedAt) < s.settings.TeardownTimeout {
		if err := s.store.LookAgain(ctx, t.ID, s.now().Add(s.settings.HealthEvery)); err != nil {
			return fmt.Errorf("holding teardown %s: %w", t.ID, err)
		}
		return failed
	}
	reason := "namespace " + t.Environment.Namespace + " is still there after " + s.settings.TeardownTimeout.String()
	if len(remaining) > 0 {
		names := make([]string, 0, remainingShown)
		for _, o := range remaining[:min(len(remaining), remainingShown)] {
			names = append(names, o.String())
		}
		if len(remaining) > remainingShown {
			names = append(names, fmt.Sprintf("and %d more", len(remaining)-remainingShown))
		}
		remain := fmt.Sprintf("%d objects remain", len(remaining))
		if len(remaining) == 1 {
			remain = "1 object remains"
		}
		reason = fmt.Sprintf("%s after %s: %s", remain, s.settings.TeardownTimeout, strings.Join(names, ", "))
	}
	if failed != nil {
		reason += "; the last attempt failed: " + failed.Error()
	}
	return s.endTeardown(ctx, t, remaining, code.DeliveryFailed, reason)
}

// endTeardown ends t in state with reason, its environment holding remaining.
func (s *Service) endTeardown(ctx context.Context, t Teardown, remaining []Object, state code.DeliveryState, reason string) error {
	if err := s.store.EndTeardown(ctx, t, remaining, state, reason); err != nil {
		return fmt.Errorf("ending teardown %s: %w", t.ID, err)
	}
	return nil
}

// Schedule queues each environment's resync, or its teardown once nothing gives it a branch any more, as Store's
// Schedule decides, every Resync: the controller calls it every minute.
func (s *Service) Schedule(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "deliverer.Service.Schedule")
	defer span.End()
	defer func() { record(span, err) }()
	queued, err := s.store.Schedule(ctx, s.settings.Resync)
	if err != nil {
		return fmt.Errorf("queueing the environments' resyncs: %w", err)
	}
	span.SetAttributes(attribute.Int("cobranch.deliveries.scheduled", queued))
	return nil
}
