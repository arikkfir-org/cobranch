package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/delivery"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/jobs"
)

// queueDeliveries queues, in the transaction of the push that moved e's branch, a delivery of e's commit to each of
// the branch's environments: one per target of the default branch's /cobranch.yaml whose patterns match the branch,
// made on its first delivery (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md,
// "Targets and environments"). Its roots come from the file at e's commit, which must still be the branch's head. Each
// delivery sets its target's annotation, delivery/<target>, on the commit, whose branches then get their status worked
// out again. A push into the default branch also follows its targets to the other branches (retarget). Its caller
// holds the repository's delivery queue, as lockDeliveries takes it.
func queueDeliveries(ctx context.Context, q *queries.Queries, e jobs.Event) error {
	branch, ok := git.Branch(e.Ref)
	if !ok || e.Kind != jobs.EventPush {
		return nil
	}
	tip, err := tipOf(ctx, q, e.RepoID, branch)
	if err != nil || tip.head != e.SHA {
		return err
	}
	main, err := mainSettings(ctx, q, e.RepoID)
	if err != nil {
		return err
	}
	if branch == git.DefaultBranch {
		if err := retarget(ctx, q, e, main); err != nil {
			return err
		}
	}
	targets := delivery.TargetsOf(main.Delivery, branch)
	if len(targets) == 0 {
		return nil
	}
	wait, err := waitsForMember(ctx, q, e.RepoID, e.ActorID)
	if err != nil {
		return err
	}
	if err := queueBranch(ctx, q, e, branch, tip, targets, wait); err != nil {
		return err
	}
	return restatusAt(ctx, q, e.RepoID, e.SHA)
}

// queueBranch queues a delivery of branch's tip, e's commit, to each of targets, waiting for a member when wait is
// true.
func queueBranch(ctx context.Context, q *queries.Queries, e jobs.Event, branch string, tip branchTip, targets []code.Target, wait bool) error {
	orgID, err := q.CommitOrg(ctx, e.RepoID, e.SHA)
	if err != nil {
		return fmt.Errorf("finding commit %s: %w", e.SHA, err)
	}
	slugs, err := q.Slugs(ctx, e.RepoID)
	if err != nil {
		return fmt.Errorf("reading the repository's slugs: %w", err)
	}
	for _, t := range targets {
		plan, planErr := delivery.PlanFor(tip.roots, t, delivery.Context{Branch: branch, Commit: e.SHA,
			Repository: slugs.Repo, Organization: slugs.Org})
		if tip.broken != "" {
			planErr = errors.New(tip.broken)
		}
		d := pushDelivery{orgID: orgID, event: e, branch: branch, target: t, namespace: plan.Namespace, planErr: planErr, wait: wait}
		if err := queuePushDelivery(ctx, q, d); err != nil {
			return err
		}
	}
	return nil
}

// retarget follows a push into the default branch, e, to the repository's other environments: it tears down those its
// targets no longer give a live branch, and makes those they now give, delivering each branch's head, which waits for a
// member when whoever pushed it there is outside the organization, as that push's deliveries would have.
func retarget(ctx context.Context, q *queries.Queries, e jobs.Event, main code.Settings) error {
	branches, err := q.LiveBranchHeads(ctx, e.RepoID)
	if err != nil {
		return fmt.Errorf("listing the live branches: %w", err)
	}
	live := make(map[string]bool, len(branches))
	for _, b := range branches {
		live[b.Name] = true
	}
	envs, err := q.EnvironmentsOf(ctx, e.RepoID, "")
	if err != nil {
		return fmt.Errorf("listing the environments: %w", err)
	}
	held := make(map[string]bool, len(envs))
	for _, env := range envs {
		held[env.Target+"\x00"+env.Environment.Branch] = true
		if wanted(main, env.Target, env.Environment.Branch, live[env.Environment.Branch]) {
			continue
		}
		if err := queueTeardown(ctx, q, env.Environment, env.Target, code.TriggeredByPush, &e.PushID); err != nil {
			return err
		}
	}
	for _, b := range branches {
		var targets []code.Target
		for _, t := range delivery.TargetsOf(main.Delivery, b.Name) {
			if !held[t.Name+"\x00"+b.Name] && b.Name != git.DefaultBranch {
				targets = append(targets, t)
			}
		}
		if len(targets) == 0 {
			continue
		}
		if err := queueHead(ctx, q, e, b.Name, b.Head, targets); err != nil {
			return err
		}
	}
	return nil
}

// queueHead queues a delivery of branch's head to each of targets, for the push into the default branch e that gave
// them the branch.
func queueHead(ctx context.Context, q *queries.Queries, e jobs.Event, branch, head string, targets []code.Target) error {
	tip, err := tipOf(ctx, q, e.RepoID, branch)
	if err != nil {
		return err
	}
	ref := "refs/heads/" + branch
	wait, err := headWaits(ctx, q, e.RepoID, ref, head)
	if err != nil {
		return err
	}
	at := jobs.Event{Kind: jobs.EventPush, RepoID: e.RepoID, PushID: e.PushID, ActorID: e.ActorID, Ref: ref, SHA: head}
	if err := queueBranch(ctx, q, at, branch, tip, targets, wait); err != nil {
		return err
	}
	return restatusAt(ctx, q, e.RepoID, head)
}

// wanted reports whether main's targets still give target's environment of branch, live when it still is.
func wanted(main code.Settings, target, branch string, live bool) bool {
	if !live {
		return false
	}
	for _, t := range delivery.TargetsOf(main.Delivery, branch) {
		if t.Name == target {
			return true
		}
	}
	return false
}

// tearDownBranch queues, in the transaction of a push that deleted branch, the teardown of each of its environments,
// unless the branch is live again: a later push that another push worker processed first recreated it.
func tearDownBranch(ctx context.Context, q *queries.Queries, repoID uuid.UUID, branch string, pushID uuid.UUID) error {
	tip, err := tipOf(ctx, q, repoID, branch)
	if err != nil || tip.head != "" {
		return err
	}
	envs, err := q.EnvironmentsOf(ctx, repoID, branch)
	if err != nil {
		return fmt.Errorf("listing branch %s's environments: %w", branch, err)
	}
	for _, env := range envs {
		if err := queueTeardown(ctx, q, env.Environment, env.Target, code.TriggeredByPush, &pushID); err != nil {
			return err
		}
	}
	return nil
}

// queueTeardown queues env's teardown, superseding what it has queued, unless a teardown of it is already queued or
// deleting.
func queueTeardown(ctx context.Context, q *queries.Queries, env queries.Environment, target string, trigger code.DeliveryTrigger,
	pushID *uuid.UUID) error {
	active, err := q.HasActiveTeardown(ctx, env.ID)
	if err != nil || active {
		return wrapIf(err, "finding environment "+env.ID.String()+"'s teardown")
	}
	_, err = enqueue(ctx, q, queuedDelivery{env: env, target: target, kind: code.Teardown, trigger: trigger, pushID: pushID,
		state: code.DeliveryQueued})
	return err
}

// scheduleBatch is the most environments one Schedule looks at; the rest wait for its next call.
const scheduleBatch = 100

// Schedule queues, for each live environment the controller's schedule is due to look at, its teardown when its
// branch is gone or the default branch's targets no longer give it one, and otherwise its resync, if it holds a
// revision and isn't paused, each in a transaction of its own that holds the repository's delivery queue
// (deliverer.Store). It goes on past an environment it fails to look at, and answers how many it queued.
func (s *DeliveryStore) Schedule(ctx context.Context, every time.Duration) (int, error) {
	due, err := s.q.DueEnvironments(ctx, every.Seconds(), scheduleBatch)
	if err != nil {
		return 0, fmt.Errorf("listing the environments due: %w", err)
	}
	queued := 0
	var errs []error
	for _, d := range due {
		err := transact(ctx, s.pool, s.q, "scheduling environment "+d.ID.String(), uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
			ok, err := scheduleEnvironment(ctx, q, d.RepoID, d.ID)
			if ok {
				queued++
			}
			return err
		})
		errs = append(errs, err)
	}
	return queued, errors.Join(errs...)
}

// scheduleEnvironment queues one environment's teardown or resync, as Schedule says, unless it ended or a delivery of
// it started meanwhile; it answers whether it queued one.
func scheduleEnvironment(ctx context.Context, q *queries.Queries, repoID, envID uuid.UUID) (bool, error) {
	if err := q.LockDeliveryQueue(ctx, repoID); err != nil {
		return false, fmt.Errorf("locking the repository's deliveries: %w", err)
	}
	row, err := q.EnvironmentForUpdate(ctx, repoID, envID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading environment %s: %w", envID, err)
	}
	active, err := q.HasActiveDelivery(ctx, envID)
	if err != nil || active {
		return false, wrapIf(err, "finding environment "+envID.String()+"'s deliveries")
	}
	main, err := mainSettings(ctx, q, repoID)
	if err != nil {
		return false, err
	}
	env := row.Environment
	tip, err := tipOf(ctx, q, repoID, env.Branch)
	if err != nil {
		return false, err
	}
	if !wanted(main, row.Target, env.Branch, tip.head != "") {
		_, err := enqueue(ctx, q, queuedDelivery{env: env, target: row.Target, kind: code.Teardown, trigger: code.TriggeredBySchedule,
			state: code.DeliveryQueued})
		return err == nil, err
	}
	if env.Paused || env.Revision == nil {
		return false, nil
	}
	_, err = enqueue(ctx, q, queuedDelivery{env: env, target: row.Target, kind: code.Resync, sha: *env.Revision,
		trigger: code.TriggeredBySchedule, state: code.DeliveryQueued})
	return err == nil, err
}

// lockDeliveries takes the repository's delivery queue for a push, in its transaction, when the default branch's
// /cobranch.yaml declares targets or an environment lives: before any of the push's steps locks a branch, since a
// redeliver, pause or resume takes the queue before it locks the branches at its commit, and a push that took them in
// the other order could deadlock with one. A repository that delivers nothing never takes it.
func lockDeliveries(ctx context.Context, q *queries.Queries, repoID uuid.UUID) error {
	main, err := mainSettings(ctx, q, repoID)
	if err != nil {
		return err
	}
	if len(main.Delivery.Targets) == 0 {
		live, err := q.HasLiveEnvironments(ctx, repoID)
		if err != nil || !live {
			return wrapIf(err, "finding the repository's environments")
		}
	}
	if err := q.LockDeliveryQueue(ctx, repoID); err != nil {
		return fmt.Errorf("locking the repository's deliveries: %w", err)
	}
	return nil
}

// branchTip is a live branch's head and the delivery roots of the /cobranch.yaml there: none without the file, and
// the file's error when it doesn't parse.
type branchTip struct {
	head   string
	roots  []code.Root
	broken string
}

// tipOf reads a live branch's tip; an empty head without the branch.
func tipOf(ctx context.Context, q *queries.Queries, repoID uuid.UUID, branch string) (branchTip, error) {
	row, err := q.BranchSettings(ctx, repoID, branch)
	if errors.Is(err, pgx.ErrNoRows) {
		return branchTip{}, nil
	}
	if err != nil {
		return branchTip{}, fmt.Errorf("reading the settings of branch %s: %w", branch, err)
	}
	t := branchTip{head: row.Head}
	switch {
	case row.Error != nil && *row.Error != "":
		t.broken = *row.Error
	case row.Settings != nil:
		var settings code.Settings
		if err := json.Unmarshal(row.Settings, &settings); err != nil {
			return branchTip{}, fmt.Errorf("decoding the settings of branch %s: %w", branch, err)
		}
		t.roots = settings.Delivery.Roots
	}
	return t, nil
}

// pushDelivery is a delivery queuePushDelivery queues: of event's commit of branch to its environment of target, in
// namespace when it is made; failed for planErr; waiting for a member when wait is true.
type pushDelivery struct {
	orgID     uuid.UUID
	event     jobs.Event
	branch    string
	target    code.Target
	namespace string
	planErr   error
	wait      bool
}

// queuePushDelivery queues one delivery of a push, unless its environment has one at the same commit that hasn't
// ended: one waiting for a member gives way to one that doesn't, which supersedes it. An environment whose cluster's
// namespace another environment holds is refused, and a paused one takes none; either way its target's annotation on
// the commit says so.
func queuePushDelivery(ctx context.Context, q *queries.Queries, d pushDelivery) error {
	name := code.DeliveryAnnotation(d.target.Name)
	targetID, err := targetPrincipal(ctx, q, d.orgID, d.event.RepoID, d.target.Name)
	if err != nil {
		return err
	}
	env, refused, err := environmentOf(ctx, q, d, targetID)
	switch {
	case err != nil:
		return err
	case refused != "":
		_, err := setJobAnnotation(ctx, q, d.orgID, targetID, d.event.RepoID, d.event.SHA, name, deliveryAnnotation(code.DeliveryFailed, refused))
		return err
	case env.Paused:
		if err := q.MarkOutOfSync(ctx, env.ID); err != nil {
			return fmt.Errorf("marking environment %s out of sync: %w", env.ID, err)
		}
		_, err := setJobAnnotation(ctx, q, d.orgID, targetID, d.event.RepoID, d.event.SHA, name,
			code.AnnotationInput{Status: code.AnnotationPending, Summary: "The environment is paused"})
		return err
	}
	sha := d.event.SHA
	active, err := q.ActiveDeliveryAt(ctx, env.ID, &sha)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return fmt.Errorf("finding environment %s's deliveries at %s: %w", env.ID, sha, err)
	case code.DeliveryState(active.State) != code.DeliveryWaiting || d.wait:
		return nil
	}
	state, reason := code.DeliveryQueued, ""
	switch {
	case d.planErr != nil:
		state, reason = code.DeliveryFailed, d.planErr.Error()
	case d.wait:
		state, reason = code.DeliveryWaiting, reasonWaiting
	}
	pushID := d.event.PushID
	_, err = enqueue(ctx, q, queuedDelivery{env: env, target: d.target.Name, kind: code.DeliverCommit, sha: sha, trigger: code.TriggeredByPush,
		pushID: &pushID, state: state, reason: reason})
	return err
}

// environmentOf finds the target's live environment of the push's branch, or makes it in the namespace the push
// planned: refused, saying why, when that namespace is no DNS label or another live environment holds it in the
// target's cluster.
func environmentOf(ctx context.Context, q *queries.Queries, d pushDelivery, targetID uuid.UUID) (queries.Environment, string, error) {
	env, err := q.LiveEnvironment(ctx, targetID, d.branch)
	if err == nil || !errors.Is(err, pgx.ErrNoRows) {
		return env, "", wrapIf(err, "finding the environment of "+d.target.Name+" and "+d.branch)
	}
	if d.namespace == "" {
		return env, capitalize(d.planErr.Error()), nil
	}
	holder, err := q.NamespaceHolder(ctx, d.target.Cluster, d.namespace)
	switch {
	case err == nil && holder.RepoID == d.event.RepoID:
		return env, fmt.Sprintf("Namespace %s is environment %s of %s's", d.namespace, holder.Target, holder.Branch), nil
	case err == nil:
		return env, fmt.Sprintf("Namespace %s is another repository's environment's", d.namespace), nil
	case !errors.Is(err, pgx.ErrNoRows):
		return env, "", fmt.Errorf("finding who holds namespace %s: %w", d.namespace, err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return env, "", fmt.Errorf("making an ID: %w", err)
	}
	env, err = q.InsertEnvironment(ctx, queries.InsertEnvironmentParams{ID: id, OrgID: d.orgID, RepoID: d.event.RepoID,
		TargetID: targetID, Branch: d.branch, Cluster: d.target.Cluster, Namespace: d.namespace})
	return env, "", wrapIf(err, "making the environment of "+d.target.Name+" and "+d.branch)
}

// targetPrincipal finds the principal of the repository's target of name, making it on the target's first delivery,
// as jobPrincipal does a job's.
func targetPrincipal(ctx context.Context, q *queries.Queries, orgID, repoID uuid.UUID, name string) (uuid.UUID, error) {
	id, err := q.TargetPrincipal(ctx, repoID, name)
	if err == nil || !errors.Is(err, pgx.ErrNoRows) {
		return id, wrapIf(err, "finding target "+name)
	}
	if id, err = uuid.NewV7(); err != nil {
		return uuid.Nil, fmt.Errorf("making an ID: %w", err)
	}
	inserted := transact(ctx, nil, q, "making target "+name+"'s principal", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		return q.InsertTarget(ctx, queries.InsertTargetParams{ID: id, Name: name, OrgID: orgID, RepoID: repoID})
	})
	var conflict *ConflictError
	if errors.As(inserted, &conflict) {
		id, err = q.TargetPrincipal(ctx, repoID, name)
		return id, wrapIf(err, "finding target "+name)
	}
	return id, inserted
}

// queuedDelivery is a delivery enqueue records: of kind, to env, its target's name, of commit sha (none for a
// teardown), for trigger, from a push or started by someone, in state for reason.
type queuedDelivery struct {
	env       queries.Environment
	target    string
	kind      code.DeliveryKind
	sha       string
	trigger   code.DeliveryTrigger
	pushID    *uuid.UUID
	startedBy *uuid.UUID
	state     code.DeliveryState
	reason    string
}

// enqueue numbers and records a delivery, superseding the environment's deliveries that haven't started applying. A
// commit's sets the target's annotation on the commit, failing it, and the delivery, when another principal holds it
// there. The environment turns Out of sync, but for a resync, which brings nothing newer. It answers the delivery's
// number.
func enqueue(ctx context.Context, q *queries.Queries, d queuedDelivery) (int64, error) {
	number, err := q.NextDeliveryNumber(ctx, d.env.RepoID)
	if err != nil {
		return 0, fmt.Errorf("numbering the delivery: %w", err)
	}
	reason := fmt.Sprintf("superseded by #%d", number)
	if err := cancelDeliveries(ctx, q, d.env, d.target, reason, d.sha, true); err != nil {
		return 0, err
	}
	if d.kind == code.DeliverCommit {
		name := code.DeliveryAnnotation(d.target)
		taken, err := setJobAnnotation(ctx, q, d.env.OrgID, d.env.TargetID, d.env.RepoID, d.sha, name, deliveryAnnotation(d.state, d.reason))
		switch {
		case err != nil:
			return 0, err
		case taken != "":
			d.state, d.reason = code.DeliveryFailed, "another principal, "+taken+", holds annotation "+name+" on this commit"
		}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return 0, fmt.Errorf("making an ID: %w", err)
	}
	var sha *string
	if d.kind != code.Teardown {
		sha = &d.sha
	}
	if err := q.InsertDelivery(ctx, queries.InsertDeliveryParams{ID: id, OrgID: d.env.OrgID, RepoID: d.env.RepoID, Number: number,
		EnvironmentID: d.env.ID, Kind: string(d.kind), Sha: sha, Trigger: string(d.trigger), PushID: d.pushID,
		State: string(d.state), Reason: d.reason, StartedBy: d.startedBy}); err != nil {
		return 0, fmt.Errorf("queueing delivery #%d of %s: %w", number, d.target, err)
	}
	if d.kind == code.Resync {
		return number, nil
	}
	if err := q.MarkOutOfSync(ctx, d.env.ID); err != nil {
		return 0, fmt.Errorf("marking environment %s out of sync: %w", d.env.ID, err)
	}
	return number, nil
}

// cancelDeliveries cancels the environment's deliveries that haven't started applying for reason, its queued teardown
// too when teardowns is true, failing its target's annotation on each one's commit, whose branches get their status
// worked out again, unless it is sha, whose caller does that.
func cancelDeliveries(ctx context.Context, q *queries.Queries, env queries.Environment, target, reason, sha string, teardowns bool) error {
	rows, err := q.SupersedeDeliveries(ctx, reason, env.ID, teardowns)
	if err != nil {
		return fmt.Errorf("cancelling environment %s's deliveries: %w", env.ID, err)
	}
	for _, row := range rows {
		if code.DeliveryKind(row.Kind) != code.DeliverCommit || row.Sha == nil {
			continue // only a delivery of a commit reports on it
		}
		in := deliveryAnnotation(code.DeliveryCancelled, reason)
		if _, err := setJobAnnotation(ctx, q, env.OrgID, env.TargetID, env.RepoID, *row.Sha, code.DeliveryAnnotation(target), in); err != nil {
			return err
		}
		if *row.Sha != sha {
			if err := restatusAt(ctx, q, env.RepoID, *row.Sha); err != nil {
				return err
			}
		}
	}
	return nil
}

// deliveryAnnotation is what a delivery's target's annotation says of a delivery that just reached state, for reason.
func deliveryAnnotation(state code.DeliveryState, reason string) code.AnnotationInput {
	switch state {
	case code.DeliveryQueued:
		return code.AnnotationInput{Status: code.AnnotationPending, Summary: "Queued"}
	case code.DeliveryWaiting:
		return code.AnnotationInput{Status: code.AnnotationPending, Summary: capitalize(reason)}
	case code.DeliveryRendering, code.DeliveryApplying:
		return code.AnnotationInput{Status: code.AnnotationInProgress, Summary: capitalize(string(state))}
	case code.DeliverySucceeded:
		return code.AnnotationInput{Status: code.AnnotationSuccessful, Summary: capitalize(reason)}
	}
	return code.AnnotationInput{Status: code.AnnotationFailed, Summary: capitalize(reason)}
}
