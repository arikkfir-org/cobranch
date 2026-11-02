package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/repos"
)

// Environments lists the repository's live environments, by target and then branch, of branch unless it is empty,
// each with its newest delivery (code.Store).
func (s *CodeStore) Environments(ctx context.Context, repoID uuid.UUID, branch string) ([]code.Environment, error) {
	rows, err := s.q.EnvironmentsOf(ctx, repoID, branch)
	if err != nil {
		return nil, fmt.Errorf("listing the environments: %w", err)
	}
	out := make([]code.Environment, 0, len(rows))
	for _, r := range rows {
		out = append(out, environmentView(r.Environment, r.Target))
	}
	return out, s.withLatest(ctx, out)
}

// Environment finds one of the repository's live environments with its newest delivery, or repos.ErrNotFound
// (code.Store).
func (s *CodeStore) Environment(ctx context.Context, repoID, id uuid.UUID) (code.Environment, error) {
	row, err := s.q.EnvironmentByID(ctx, repoID, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return code.Environment{}, repos.ErrNotFound
	}
	if err != nil {
		return code.Environment{}, fmt.Errorf("finding environment %s: %w", id, err)
	}
	out := []code.Environment{environmentView(row.Environment, row.Target)}
	return out[0], s.withLatest(ctx, out)
}

// withLatest sets each environment's newest delivery.
func (s *CodeStore) withLatest(ctx context.Context, envs []code.Environment) error {
	if len(envs) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(envs))
	for i, e := range envs {
		ids[i] = e.ID
	}
	rows, err := s.q.LatestDeliveries(ctx, ids)
	if err != nil {
		return fmt.Errorf("finding the environments' deliveries: %w", err)
	}
	latest := make(map[uuid.UUID]code.Delivery, len(rows))
	for _, r := range rows {
		latest[r.Delivery.EnvironmentID] = deliveryView(r.Delivery, r.Target, r.Branch, r.StartedByName)
	}
	for i := range envs {
		if d, ok := latest[envs[i].ID]; ok {
			envs[i].Latest = &d
		}
	}
	return nil
}

// Deliveries lists at most limit of a live environment's deliveries, newest first, numbered before before (0 for the
// newest), or repos.ErrNotFound without it (code.Store).
func (s *CodeStore) Deliveries(ctx context.Context, repoID, environmentID uuid.UUID, before int64, limit int) ([]code.Delivery, error) {
	if _, err := s.q.EnvironmentByID(ctx, repoID, environmentID); errors.Is(err, pgx.ErrNoRows) {
		return nil, repos.ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("finding environment %s: %w", environmentID, err)
	}
	rows, err := s.q.DeliveriesOf(ctx, environmentID, before, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("listing the deliveries: %w", err)
	}
	out := make([]code.Delivery, 0, len(rows))
	for _, r := range rows {
		out = append(out, deliveryView(r.Delivery, r.Target, r.Branch, r.StartedByName))
	}
	return out, nil
}

// Delivery finds one of the repository's deliveries by number, or repos.ErrNotFound (code.Store).
func (s *CodeStore) Delivery(ctx context.Context, repoID uuid.UUID, number int64) (code.Delivery, error) {
	row, err := s.q.DeliveryByNumber(ctx, repoID, number)
	if errors.Is(err, pgx.ErrNoRows) {
		return code.Delivery{}, repos.ErrNotFound
	}
	if err != nil {
		return code.Delivery{}, fmt.Errorf("finding delivery #%d: %w", number, err)
	}
	return deliveryView(row.Delivery, row.Target, row.Branch, row.StartedByName), nil
}

// Redeliver queues, for actor, a delivery of the head of a live environment's branch, superseding its deliveries
// that haven't started applying, in one transaction that acts for actor and appends event delivery.redelivered
// (code.Store).
func (s *CodeStore) Redeliver(ctx context.Context, actor uuid.UUID, repoID, environmentID uuid.UUID) (code.Delivery, error) {
	var number int64
	err := transact(ctx, s.pool, s.q, "redelivering", actor, func(ctx context.Context, q *queries.Queries) error {
		row, err := environmentForUpdate(ctx, q, repoID, environmentID)
		if err != nil {
			return err
		}
		if row.Environment.Paused {
			return code.ErrEnvironmentPaused
		}
		if number, err = deliverHead(ctx, q, row, actor, code.TriggeredByRedeliver); err != nil {
			return err
		}
		return orgEvent(ctx, q, &row.Environment.OrgID, actor, "delivery.redelivered", environmentID,
			map[string]any{"repo": repoID, "number": number})
	})
	if err != nil {
		return code.Delivery{}, err
	}
	return s.Delivery(ctx, repoID, number)
}

// PauseEnvironment pauses a live environment for actor, cancelling its deliveries that haven't started applying, in
// one transaction that acts for actor and appends event environment.paused (code.Store).
func (s *CodeStore) PauseEnvironment(ctx context.Context, actor uuid.UUID, repoID, environmentID uuid.UUID) (code.Environment, error) {
	err := transact(ctx, s.pool, s.q, "pausing the environment", actor, func(ctx context.Context, q *queries.Queries) error {
		row, err := environmentForUpdate(ctx, q, repoID, environmentID)
		if err != nil {
			return err
		}
		env := row.Environment
		if env.Paused {
			return code.ErrEnvironmentPaused
		}
		if err := q.SetEnvironmentPaused(ctx, true, env.ID); err != nil {
			return fmt.Errorf("pausing environment %s: %w", env.ID, err)
		}
		name, err := q.PrincipalName(ctx, actor)
		if err != nil {
			return fmt.Errorf("naming who pauses: %w", err)
		}
		if err := cancelDeliveries(ctx, q, env, row.Target, "paused by "+name, ""); err != nil {
			return err
		}
		return orgEvent(ctx, q, &env.OrgID, actor, "environment.paused", env.ID, map[string]any{"repo": repoID})
	})
	if err != nil {
		return code.Environment{}, err
	}
	return s.Environment(ctx, repoID, environmentID)
}

// ResumeEnvironment resumes a paused environment for actor and queues a delivery of its branch's head, in one
// transaction that acts for actor and appends event environment.resumed (code.Store).
func (s *CodeStore) ResumeEnvironment(ctx context.Context, actor uuid.UUID, repoID, environmentID uuid.UUID) (code.Environment, error) {
	err := transact(ctx, s.pool, s.q, "resuming the environment", actor, func(ctx context.Context, q *queries.Queries) error {
		row, err := environmentForUpdate(ctx, q, repoID, environmentID)
		if err != nil {
			return err
		}
		if !row.Environment.Paused {
			return code.ErrEnvironmentNotPaused
		}
		if err := q.SetEnvironmentPaused(ctx, false, environmentID); err != nil {
			return fmt.Errorf("resuming environment %s: %w", environmentID, err)
		}
		number, err := deliverHead(ctx, q, row, actor, code.TriggeredByResume)
		if err != nil {
			return err
		}
		return orgEvent(ctx, q, &row.Environment.OrgID, actor, "environment.resumed", environmentID,
			map[string]any{"repo": repoID, "number": number})
	})
	if err != nil {
		return code.Environment{}, err
	}
	return s.Environment(ctx, repoID, environmentID)
}

// StartDelivery queues a waiting delivery for actor, in one transaction that acts for actor and appends event
// delivery.started; its target's annotation stays Pending, now Queued (code.Store).
func (s *CodeStore) StartDelivery(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, number int64) (code.Delivery, error) {
	err := transact(ctx, s.pool, s.q, "starting the delivery", actor, func(ctx context.Context, q *queries.Queries) error {
		row, err := q.DeliveryForUpdate(ctx, repoID, number)
		if errors.Is(err, pgx.ErrNoRows) {
			return repos.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("finding delivery #%d: %w", number, err)
		}
		d := row.Delivery
		if code.DeliveryState(d.State) != code.DeliveryWaiting || d.Sha == nil {
			return code.ErrDeliveryNotWaiting
		}
		if err := q.StartWaitingDelivery(ctx, &actor, d.ID); err != nil {
			return fmt.Errorf("starting delivery #%d: %w", number, err)
		}
		in := deliveryAnnotation(code.DeliveryQueued, "")
		if _, err := setJobAnnotation(ctx, q, d.OrgID, row.TargetID, repoID, *d.Sha, code.DeliveryAnnotation(row.Target), in); err != nil {
			return err
		}
		return orgEvent(ctx, q, &d.OrgID, actor, "delivery.started", d.ID, map[string]any{"repo": repoID, "number": number})
	})
	if err != nil {
		return code.Delivery{}, err
	}
	return s.Delivery(ctx, repoID, number)
}

// deliverHead queues, for actor, a delivery of the head of the environment's branch, superseding every one of its
// deliveries that hasn't started applying, and works out the status of the branches at the head again; it answers the
// delivery's number. A branch that is gone is repos.ErrNotFound.
func deliverHead(ctx context.Context, q *queries.Queries, row queries.EnvironmentForUpdateRow, actor uuid.UUID, trigger code.DeliveryTrigger) (int64, error) {
	env := row.Environment
	head, err := tipOf(ctx, q, env.RepoID, env.Branch)
	if err != nil {
		return 0, err
	}
	if head.head == "" {
		return 0, repos.ErrNotFound
	}
	if err := q.LockDeliveryQueue(ctx, env.RepoID); err != nil {
		return 0, fmt.Errorf("locking the repository's deliveries: %w", err)
	}
	number, err := enqueue(ctx, q, queuedDelivery{env: env, target: row.Target, sha: head.head, trigger: trigger,
		startedBy: &actor, state: code.DeliveryQueued})
	if err != nil {
		return 0, err
	}
	return number, restatusAt(ctx, q, env.RepoID, head.head)
}

// environmentForUpdate finds a live environment and locks it, or answers repos.ErrNotFound.
func environmentForUpdate(ctx context.Context, q *queries.Queries, repoID, id uuid.UUID) (queries.EnvironmentForUpdateRow, error) {
	row, err := q.EnvironmentForUpdate(ctx, repoID, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, repos.ErrNotFound
	}
	if err != nil {
		return row, fmt.Errorf("finding environment %s: %w", id, err)
	}
	return row, nil
}

// environmentView is the domain's view of an environments row, its target named target.
func environmentView(e queries.Environment, target string) code.Environment {
	out := code.Environment{ID: e.ID, Target: code.Principal{ID: e.TargetID, DisplayName: target}, Branch: e.Branch,
		Cluster: e.Cluster, Namespace: e.Namespace, SyncedAt: e.SyncedAt, Sync: code.Sync(e.Sync), Health: code.Health(e.Health),
		Paused: e.Paused, CreatedAt: e.CreatedAt, Version: e.Version}
	if e.Revision != nil {
		out.Revision = *e.Revision
	}
	return out
}

// deliveryView is the domain's view of a deliveries row, of target's environment of branch, started by who
// startedBy names, if anyone.
func deliveryView(d queries.Delivery, target, branch string, startedBy *string) code.Delivery {
	out := code.Delivery{ID: d.ID, Number: d.Number, EnvironmentID: d.EnvironmentID, Target: target, Branch: branch,
		Kind: code.DeliveryKind(d.Kind), Trigger: code.DeliveryTrigger(d.Trigger), PushID: d.PushID,
		State: code.DeliveryState(d.State), Reason: d.Reason, StartedBy: principalOf(d.StartedBy, startedBy),
		QueuedAt: d.QueuedAt, StartedAt: d.StartedAt, EndedAt: d.EndedAt, Version: d.Version}
	if d.Sha != nil {
		out.SHA = *d.Sha
	}
	return out
}
