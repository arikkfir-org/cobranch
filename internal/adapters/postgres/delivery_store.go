package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/deliverer"
)

// DeliveryStore keeps the deliveries the controller delivers (db/migrations/00029_environments.sql), their tokens,
// renders and the environments' inventories (db/migrations/00045_delivery_controller.sql): deliverer.Store on
// PostgreSQL. What it changes is Cobranch's own doing, which names nobody; each change of a delivery's state sets its
// target's annotation on its commit to match, as the target, in the same transaction.
type DeliveryStore struct {
	pool *pgxpool.Pool
	q    *queries.Queries
}

// NewDeliveryStore returns the deliveries' store on pool's database.
func NewDeliveryStore(pool *pgxpool.Pool) *DeliveryStore {
	return &DeliveryStore{pool: pool, q: queries.New(pool)}
}

var _ deliverer.Store = (*DeliveryStore)(nil)

// targeted is a delivery whose state just changed: what its target's annotation needs.
type targeted struct {
	OrgID, RepoID uuid.UUID
	Sha           string
	TargetID      uuid.UUID
	Target        string
}

// annotate sets the delivery's target's annotation on its commit to in, as the target, and works out the status of
// the branches there again. Another principal holding the annotation changes nothing: the delivery failed as it was
// queued then. A teardown has no commit to annotate.
func annotate(ctx context.Context, q *queries.Queries, d targeted, in code.AnnotationInput) error {
	if d.Sha == "" {
		return nil
	}
	if _, err := setJobAnnotation(ctx, q, d.OrgID, d.TargetID, d.RepoID, d.Sha, code.DeliveryAnnotation(d.Target), in); err != nil {
		return err
	}
	return restatusAt(ctx, q, d.RepoID, d.Sha)
}

// Claim claims the queued delivery that waited longest, that no controller holds and whose environment isn't applying
// or deleting another, until leaseUntil (deliverer.Store).
func (s *DeliveryStore) Claim(ctx context.Context, leaseUntil time.Time) (deliverer.Delivery, bool, error) {
	row, err := s.q.ClaimDelivery(ctx, leaseUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return deliverer.Delivery{}, false, nil
	}
	if err != nil {
		return deliverer.Delivery{}, false, fmt.Errorf("claiming a queued delivery: %w", err)
	}
	return deliverer.Delivery{ID: row.ID, OrgID: row.OrgID, RepoID: row.RepoID, EnvironmentID: row.EnvironmentID, TargetID: row.TargetID,
		Number: row.Number, Kind: code.DeliveryKind(row.Kind), Target: row.Target, Branch: row.Branch, SHA: row.Sha, OrgSlug: row.OrgSlug,
		RepoSlug: row.RepoSlug, Cluster: row.Cluster, Namespace: row.Namespace}, true, nil
}

// Token records the delivery's token by its hash and hint, until expiresAt, replacing one made before
// (deliverer.Store).
func (s *DeliveryStore) Token(ctx context.Context, deliveryID uuid.UUID, hash []byte, hint string, expiresAt time.Time) error {
	err := s.q.InsertDeliveryToken(ctx, queries.InsertDeliveryTokenParams{DeliveryID: deliveryID, TokenHash: hash, Hint: hint, ExpiresAt: expiresAt})
	return wrapIf(err, "recording delivery "+deliveryID.String()+"'s token")
}

// Defer puts a queued delivery off until until, its target's annotation saying why (deliverer.Store).
func (s *DeliveryStore) Defer(ctx context.Context, deliveryID uuid.UUID, until time.Time, reason string) error {
	return transact(ctx, s.pool, s.q, "putting the delivery off", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		row, err := q.DeferDelivery(ctx, until, reason, deliveryID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("putting delivery %s off: %w", deliveryID, err)
		}
		return annotate(ctx, q, targeted(row), code.AnnotationInput{Status: code.AnnotationPending, Summary: capitalize(reason)})
	})
}

// MarkRendering marks a queued delivery rendering and its target's annotation In progress (deliverer.Store).
func (s *DeliveryStore) MarkRendering(ctx context.Context, deliveryID uuid.UUID) (bool, error) {
	marked := false
	err := transact(ctx, s.pool, s.q, "marking the delivery rendering", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		row, err := q.MarkDeliveryRendering(ctx, deliveryID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("marking delivery %s rendering: %w", deliveryID, err)
		}
		marked = true
		return annotate(ctx, q, targeted(row), deliveryAnnotation(code.DeliveryRendering, ""))
	})
	return marked, err
}

// MarkDeleting marks a queued teardown deleting (deliverer.Store).
func (s *DeliveryStore) MarkDeleting(ctx context.Context, deliveryID uuid.UUID) (bool, error) {
	n, err := s.q.MarkDeliveryDeleting(ctx, deliveryID)
	if err != nil {
		return false, fmt.Errorf("marking teardown %s deleting: %w", deliveryID, err)
	}
	return n > 0, nil
}

// Working lists the deliveries that render, those whose apply a controller left, and the teardowns no controller holds
// (deliverer.Store).
func (s *DeliveryStore) Working(ctx context.Context) ([]deliverer.Working, error) {
	rows, err := s.q.WorkingDeliveries(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing the deliveries that render and apply: %w", err)
	}
	out := make([]deliverer.Working, 0, len(rows))
	for _, r := range rows {
		out = append(out, deliverer.Working{ID: r.ID, OrgID: r.OrgID, RepoID: r.RepoID, Number: r.Number, Kind: code.DeliveryKind(r.Kind),
			State: code.DeliveryState(r.State), StartedAt: r.StartedAt, Rendered: r.Rendered})
	}
	return out, nil
}

// TakeApply marks a delivery that rendered, a queued resync, or a delivery whose apply a controller left, applying until
// leaseUntil and, for a commit's, its target's annotation to match, and answers its render and what its environment
// holds (deliverer.Store).
func (s *DeliveryStore) TakeApply(ctx context.Context, deliveryID uuid.UUID, leaseUntil time.Time) (deliverer.Apply, bool, error) {
	var a deliverer.Apply
	taken := false
	err := transact(ctx, s.pool, s.q, "taking the delivery's apply", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		row, err := q.TakeDeliveryApply(ctx, leaseUntil, deliveryID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("taking delivery %s's apply: %w", deliveryID, err)
		}
		kind := code.DeliveryKind(row.Kind)
		manifests, rendered, err := renderOf(ctx, q, kind, deliveryID, row.EnvironmentID)
		if err != nil {
			return err
		}
		inventory, err := objectsOf(ctx, q, row.EnvironmentID)
		if err != nil {
			return err
		}
		a = deliverer.Apply{ID: row.ID, OrgID: row.OrgID, RepoID: row.RepoID, EnvironmentID: row.EnvironmentID, TargetID: row.TargetID,
			Number: row.Number, Kind: kind, Target: row.Target, SHA: row.Sha, Namespace: row.Namespace, Cluster: row.Cluster,
			Revision: derefString(row.Revision), Health: code.Health(row.EnvironmentHealth), Manifests: manifests, Rendered: rendered,
			Inventory: inventory}
		taken = true
		if kind != code.DeliverCommit {
			return nil
		}
		return annotate(ctx, q, targeted{OrgID: row.OrgID, RepoID: row.RepoID, Sha: row.Sha, TargetID: row.TargetID, Target: row.Target},
			deliveryAnnotation(code.DeliveryApplying, ""))
	})
	return a, taken, err
}

// renderOf is what a delivery of kind applies: its own render, or for a resync its environment's last delivery of a
// commit to succeed; false when a resync finds none.
func renderOf(ctx context.Context, q *queries.Queries, kind code.DeliveryKind, deliveryID, environmentID uuid.UUID) ([]byte, bool, error) {
	if kind != code.Resync {
		manifests, err := q.DeliveryRender(ctx, deliveryID)
		if err != nil {
			return nil, false, fmt.Errorf("reading delivery %s's render: %w", deliveryID, err)
		}
		return manifests, true, nil
	}
	manifests, err := q.LastRender(ctx, environmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading environment %s's last render: %w", environmentID, err)
	}
	return manifests, true, nil
}

// Record records how an apply ended, in one transaction: the delivery's end and its token's; the environment's
// inventory; for one that succeeded the environment's revision, health and sync, and for one that failed Out of sync;
// and, for a commit's, the target's annotation (deliverer.Store). A delivery that ended already, as another controller took its apply
// when its lease lapsed, records nothing more: the environment's inventory may be a newer delivery's by then.
func (s *DeliveryStore) Record(ctx context.Context, o deliverer.Outcome) error {
	a := o.Apply
	return transact(ctx, s.pool, s.q, "recording the delivery's apply", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		if err := q.DeleteDeliveryToken(ctx, a.ID); err != nil {
			return fmt.Errorf("deleting delivery %s's token: %w", a.ID, err)
		}
		row, err := q.FinishDelivery(ctx, string(o.State), o.Reason, a.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("ending delivery %s: %w", a.ID, err)
		}
		if err := setInventory(ctx, q, a.EnvironmentID, a.ID, o.Inventory); err != nil {
			return err
		}
		if o.State == code.DeliverySucceeded {
			err = q.SetEnvironmentDelivered(ctx, queries.SetEnvironmentDeliveredParams{Revision: a.SHA, Health: string(o.Health),
				DeliveryID: a.ID, ID: a.EnvironmentID})
		} else {
			err = q.MarkOutOfSync(ctx, a.EnvironmentID)
		}
		if err != nil {
			return fmt.Errorf("recording environment %s's delivery: %w", a.EnvironmentID, err)
		}
		if a.Kind != code.DeliverCommit {
			return nil
		}
		return annotate(ctx, q, targeted{OrgID: row.OrgID, RepoID: row.RepoID, Sha: row.Sha, TargetID: row.TargetID, Target: row.Target}, o.Report)
	})
}

// TakeTeardown holds a teardown that deletes until leaseUntil, once no controller holds it, and answers what its
// environment holds (deliverer.Store).
func (s *DeliveryStore) TakeTeardown(ctx context.Context, deliveryID uuid.UUID, leaseUntil time.Time) (deliverer.Teardown, bool, error) {
	row, err := s.q.TakeTeardown(ctx, leaseUntil, deliveryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return deliverer.Teardown{}, false, nil
	}
	if err != nil {
		return deliverer.Teardown{}, false, fmt.Errorf("taking teardown %s: %w", deliveryID, err)
	}
	inventory, err := objectsOf(ctx, s.q, row.EnvironmentID)
	if err != nil {
		return deliverer.Teardown{}, false, err
	}
	env := deliverer.Environment{ID: row.EnvironmentID, OrgID: row.OrgID, RepoID: row.RepoID, TargetID: row.TargetID, Target: row.Target,
		Cluster: row.Cluster, Namespace: row.Namespace}
	return deliverer.Teardown{ID: row.ID, OrgID: row.OrgID, RepoID: row.RepoID, Number: row.Number, Environment: env, Inventory: inventory,
		StartedAt: row.StartedAt}, true, nil
}

// LookAgain holds a teardown that deletes until at (deliverer.Store).
func (s *DeliveryStore) LookAgain(ctx context.Context, deliveryID uuid.UUID, at time.Time) error {
	return wrapIf(s.q.LeaseTeardown(ctx, at, deliveryID), "holding teardown "+deliveryID.String())
}

// EndTeardown ends a teardown in state with reason, in one transaction (deliverer.Store). One that succeeded ends its
// environment, keeping its last inventory as its history, unless a newer delivery of it waits, as its branch came back:
// that environment then holds nothing and has no revision. One that failed leaves the environment holding remaining,
// Out of sync, for the schedule to tear down again.
func (s *DeliveryStore) EndTeardown(ctx context.Context, t deliverer.Teardown, remaining []deliverer.Object, state code.DeliveryState,
	reason string) error {
	envID := t.Environment.ID
	return transact(ctx, s.pool, s.q, "ending the teardown", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.FinishDelivery(ctx, string(state), reason, t.ID); errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return fmt.Errorf("ending teardown %s: %w", t.ID, err)
		}
		if state != code.DeliverySucceeded {
			if err := setInventory(ctx, q, envID, t.ID, remaining); err != nil {
				return err
			}
			return wrapIf(q.MarkOutOfSync(ctx, envID), "marking environment "+envID.String()+" out of sync")
		}
		ended, err := q.EndEnvironment(ctx, envID, t.ID)
		switch {
		case err != nil:
			return fmt.Errorf("ending environment %s: %w", envID, err)
		case ended > 0:
			return nil
		}
		if err := setInventory(ctx, q, envID, t.ID, nil); err != nil {
			return err
		}
		return wrapIf(q.EmptyEnvironment(ctx, envID), "emptying environment "+envID.String())
	})
}

// Finish ends a delivery that hasn't ended, in state with reason, sets its target's annotation to match, turns its
// environment Out of sync and deletes its token (deliverer.Store).
func (s *DeliveryStore) Finish(ctx context.Context, deliveryID uuid.UUID, state code.DeliveryState, reason string) (bool, error) {
	finished := false
	err := transact(ctx, s.pool, s.q, "ending the delivery", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		if err := q.DeleteDeliveryToken(ctx, deliveryID); err != nil {
			return fmt.Errorf("deleting delivery %s's token: %w", deliveryID, err)
		}
		row, err := q.FinishDelivery(ctx, string(state), reason, deliveryID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("ending delivery %s: %w", deliveryID, err)
		}
		finished = true
		if err := q.MarkOutOfSync(ctx, row.EnvironmentID); err != nil {
			return fmt.Errorf("marking environment %s out of sync: %w", row.EnvironmentID, err)
		}
		if code.DeliveryKind(row.Kind) != code.DeliverCommit {
			return nil
		}
		return annotate(ctx, q, targeted{OrgID: row.OrgID, RepoID: row.RepoID, Sha: row.Sha, TargetID: row.TargetID, Target: row.Target},
			deliveryAnnotation(state, reason))
	})
	return finished, err
}

// Deliveries finds those of ids that are deliveries: where each stands, and its repository (deliverer.Store).
func (s *DeliveryStore) Deliveries(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]deliverer.Standing, error) {
	rows, err := s.q.DeliveriesStanding(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("finding the deliveries: %w", err)
	}
	out := make(map[uuid.UUID]deliverer.Standing, len(rows))
	for _, r := range rows {
		out[r.ID] = deliverer.Standing{RepoID: r.RepoID, State: code.DeliveryState(r.State)}
	}
	return out, nil
}

// DropTokens deletes the tokens of deliveries that no longer render, and those that expired (deliverer.Store).
func (s *DeliveryStore) DropTokens(ctx context.Context) (int64, error) {
	n, err := s.q.DeleteDeadDeliveryTokens(ctx)
	return n, wrapIf(err, "deleting the tokens of ended deliveries")
}

// DropRenders deletes each environment's renders past its newest keep, but those of deliveries that haven't ended and
// its last to succeed (deliverer.Store).
func (s *DeliveryStore) DropRenders(ctx context.Context, keep int) (int64, error) {
	n, err := s.q.DeleteOldRenders(ctx, int32(min(keep, math.MaxInt32))) //nolint:gosec // min caps it
	if err != nil {
		return 0, fmt.Errorf("deleting old renders: %w", err)
	}
	return n, nil
}

// Progressing lists the live environments whose health is Progressing (deliverer.Store).
func (s *DeliveryStore) Progressing(ctx context.Context) ([]deliverer.Environment, error) {
	rows, err := s.q.ProgressingEnvironments(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing the environments that are progressing: %w", err)
	}
	out := make([]deliverer.Environment, 0, len(rows))
	for _, r := range rows {
		out = append(out, deliverer.Environment{ID: r.ID, OrgID: r.OrgID, RepoID: r.RepoID, TargetID: r.TargetID, Target: r.Target,
			Cluster: r.Cluster, Namespace: r.Namespace})
	}
	return out, nil
}

// Objects lists what an environment holds, in order (deliverer.Store).
func (s *DeliveryStore) Objects(ctx context.Context, environmentID uuid.UUID) ([]deliverer.Object, error) {
	return objectsOf(ctx, s.q, environmentID)
}

// SetHealth records the health of an environment's objects and its own and, when its own changed, sets the annotation
// of its revision's delivery to report (deliverer.Store).
func (s *DeliveryStore) SetHealth(ctx context.Context, environmentID uuid.UUID, objects []deliverer.Object, health code.Health,
	report code.AnnotationInput) error {
	return transact(ctx, s.pool, s.q, "recording the environment's health", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		p := queries.SetObjectHealthsParams{EnvironmentID: environmentID}
		for i, o := range objects {
			p.Positions, p.Healths, p.Messages = append(p.Positions, int32(i)), append(p.Healths, string(o.Health)), append(p.Messages, o.Message) //nolint:gosec // an inventory holds far fewer than 2^31 objects
		}
		if err := q.SetObjectHealths(ctx, p); err != nil {
			return fmt.Errorf("recording environment %s's objects' health: %w", environmentID, err)
		}
		row, err := q.SetEnvironmentHealth(ctx, string(health), environmentID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("recording environment %s's health: %w", environmentID, err)
		case row.Revision == nil:
			return nil
		}
		return annotate(ctx, q, targeted{OrgID: row.OrgID, RepoID: row.RepoID, Sha: *row.Revision, TargetID: row.TargetID, Target: row.Target}, report)
	})
}

// objectsOf reads an environment's inventory, in order.
func objectsOf(ctx context.Context, q *queries.Queries, environmentID uuid.UUID) ([]deliverer.Object, error) {
	rows, err := q.EnvironmentObjects(ctx, environmentID)
	if err != nil {
		return nil, fmt.Errorf("reading environment %s's objects: %w", environmentID, err)
	}
	out := make([]deliverer.Object, 0, len(rows))
	for _, r := range rows {
		out = append(out, deliverer.Object{Group: r.ApiGroup, Version: r.Version, Kind: r.Kind, Namespace: r.Namespace, Name: r.Name,
			UID: r.Uid, Health: code.Health(r.Health), Message: r.Message})
	}
	return out, nil
}

// setInventory replaces an environment's inventory with objects, as delivery deliveryID left it.
func setInventory(ctx context.Context, q *queries.Queries, environmentID, deliveryID uuid.UUID, objects []deliverer.Object) error {
	if err := q.DeleteEnvironmentObjects(ctx, environmentID); err != nil {
		return fmt.Errorf("clearing environment %s's inventory: %w", environmentID, err)
	}
	p := queries.InsertEnvironmentObjectsParams{EnvironmentID: environmentID}
	for i, o := range objects {
		health := o.Health
		if health == "" {
			health = code.Unknown
		}
		p.Positions = append(p.Positions, int32(i)) //nolint:gosec // an inventory holds far fewer than 2^31 objects
		p.ApiGroups, p.Versions, p.Kinds = append(p.ApiGroups, o.Group), append(p.Versions, o.Version), append(p.Kinds, o.Kind)
		p.Namespaces, p.Names, p.Uids = append(p.Namespaces, o.Namespace), append(p.Names, o.Name), append(p.Uids, o.UID)
		p.DeliveryIds, p.Healths, p.Messages = append(p.DeliveryIds, deliveryID), append(p.Healths, string(health)), append(p.Messages, o.Message)
	}
	if err := q.InsertEnvironmentObjects(ctx, p); err != nil {
		return fmt.Errorf("recording environment %s's inventory: %w", environmentID, err)
	}
	return nil
}

// derefString is a nullable column's text, empty for null.
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
