-- name: TargetPrincipal :one
SELECT principal_id FROM delivery_targets WHERE repo_id = @repo_id AND name = @name;

-- InsertTarget makes a target of a repository a principal of its organization.
-- name: InsertTarget :exec
WITH principal AS (
    INSERT INTO principals (id, kind, display_name, created_at, org_id) VALUES (@id, 'target', @name, now(), @org_id)
)
INSERT INTO delivery_targets (principal_id, org_id, repo_id, name) VALUES (@id, @org_id, @repo_id, @name);

-- LockDeliveryQueue locks the repository's delivery counter, making it first, to the end of the transaction:
-- deliveries are queued for one repository at a time, so two changes can't both find no environment of a target and
-- branch, or no delivery at a commit.
-- name: LockDeliveryQueue :exec
INSERT INTO delivery_numbers (repo_id, last) VALUES (@repo_id, 0)
ON CONFLICT (repo_id) DO UPDATE SET last = delivery_numbers.last;

-- NextDeliveryNumber counts a repository's deliveries on by one.
-- name: NextDeliveryNumber :one
INSERT INTO delivery_numbers (repo_id, last) VALUES (@repo_id, 1)
ON CONFLICT (repo_id) DO UPDATE SET last = delivery_numbers.last + 1
RETURNING last;

-- LiveEnvironment finds a target's live environment of a branch.
-- name: LiveEnvironment :one
SELECT * FROM environments WHERE target_id = @target_id AND branch = @branch AND deleted_at IS NULL;

-- name: InsertEnvironment :one
INSERT INTO environments (id, org_id, repo_id, target_id, branch, cluster, namespace)
VALUES (@id, @org_id, @repo_id, @target_id, @branch, @cluster, @namespace)
RETURNING *;

-- NamespaceHolder finds the live environment, of any organization, in a cluster's namespace: the installation's own
-- cluster with a null cluster.
-- name: NamespaceHolder :one
SELECT e.id, e.repo_id, e.branch, t.name AS target
FROM environments e JOIN delivery_targets t ON t.principal_id = e.target_id
WHERE e.cluster IS NOT DISTINCT FROM sqlc.narg(cluster)::uuid AND e.namespace = @namespace AND e.deleted_at IS NULL;

-- MarkOutOfSync turns an environment Out of sync while a newer commit waits.
-- name: MarkOutOfSync :exec
UPDATE environments SET sync = 'out_of_sync' WHERE id = @id AND sync <> 'out_of_sync';

-- SetEnvironmentPaused pauses or resumes an environment.
-- name: SetEnvironmentPaused :exec
UPDATE environments SET paused = @paused WHERE id = @id;

-- SupersedeDeliveries cancels an environment's deliveries that haven't started applying, with the reason, and answers
-- the commit of each.
-- name: SupersedeDeliveries :many
UPDATE deliveries SET state = 'cancelled', reason = @reason, ended_at = now(), lease_until = NULL
WHERE environment_id = @environment_id AND state IN ('waiting', 'queued', 'rendering')
RETURNING id, sha;

-- ActiveDeliveryAt finds an environment's newest delivery of a commit that hasn't ended, locked until the transaction
-- ends.
-- name: ActiveDeliveryAt :one
SELECT id, state FROM deliveries
WHERE environment_id = @environment_id AND sha = @sha AND state IN ('waiting', 'queued', 'rendering', 'applying')
ORDER BY number DESC LIMIT 1
FOR UPDATE;

-- name: InsertDelivery :exec
INSERT INTO deliveries (id, org_id, repo_id, number, environment_id, kind, sha, trigger, push_id, state, reason, started_by, ended_at)
VALUES (@id, @org_id, @repo_id, @number, @environment_id, @kind, @sha, @trigger, @push_id, @state, @reason, @started_by,
        CASE WHEN @state::text IN ('succeeded', 'failed', 'cancelled') THEN now() END);

-- EnvironmentsOf lists a repository's live environments, by target and then branch, of one branch when it is named,
-- with their targets' names.
-- name: EnvironmentsOf :many
SELECT sqlc.embed(e), t.name AS target
FROM environments e JOIN delivery_targets t ON t.principal_id = e.target_id
WHERE e.repo_id = @repo_id AND e.deleted_at IS NULL AND (@branch::text = '' OR e.branch = @branch::text)
ORDER BY t.name, e.branch;

-- name: EnvironmentByID :one
SELECT sqlc.embed(e), t.name AS target
FROM environments e JOIN delivery_targets t ON t.principal_id = e.target_id
WHERE e.repo_id = @repo_id AND e.id = @id AND e.deleted_at IS NULL;

-- EnvironmentForUpdate finds a live environment with its target's name, and locks it until the transaction ends.
-- name: EnvironmentForUpdate :one
SELECT sqlc.embed(e), t.name AS target
FROM environments e JOIN delivery_targets t ON t.principal_id = e.target_id
WHERE e.repo_id = @repo_id AND e.id = @id AND e.deleted_at IS NULL
FOR UPDATE OF e;

-- LatestDeliveries finds the newest delivery of each of some environments, with its environment's target and branch
-- and the name of who started it.
-- name: LatestDeliveries :many
SELECT DISTINCT ON (d.environment_id) sqlc.embed(d), t.name AS target, e.branch, p.display_name AS started_by_name
FROM deliveries d JOIN environments e ON e.id = d.environment_id JOIN delivery_targets t ON t.principal_id = e.target_id
    LEFT JOIN principals p ON p.id = d.started_by
WHERE d.environment_id = ANY (@ids::uuid[])
ORDER BY d.environment_id, d.number DESC;

-- DeliveriesOf lists a page of an environment's deliveries, newest first, from before a number (0 for the newest).
-- name: DeliveriesOf :many
SELECT sqlc.embed(d), t.name AS target, e.branch, p.display_name AS started_by_name
FROM deliveries d JOIN environments e ON e.id = d.environment_id JOIN delivery_targets t ON t.principal_id = e.target_id
    LEFT JOIN principals p ON p.id = d.started_by
WHERE d.environment_id = @environment_id AND (@before::bigint = 0 OR d.number < @before::bigint)
ORDER BY d.number DESC
LIMIT @lim;

-- name: DeliveryByNumber :one
SELECT sqlc.embed(d), t.name AS target, e.branch, p.display_name AS started_by_name
FROM deliveries d JOIN environments e ON e.id = d.environment_id JOIN delivery_targets t ON t.principal_id = e.target_id
    LEFT JOIN principals p ON p.id = d.started_by
WHERE d.repo_id = @repo_id AND d.number = @number;

-- DeliveryForUpdate finds a delivery by number with its environment's target, and locks it until the transaction ends.
-- name: DeliveryForUpdate :one
SELECT sqlc.embed(d), e.target_id, t.name AS target
FROM deliveries d JOIN environments e ON e.id = d.environment_id JOIN delivery_targets t ON t.principal_id = e.target_id
WHERE d.repo_id = @repo_id AND d.number = @number
FOR UPDATE OF d;

-- StartWaitingDelivery queues a waiting delivery for whoever started it.
-- name: StartWaitingDelivery :exec
UPDATE deliveries SET state = 'queued', reason = '', started_by = @started_by, queued_at = now()
WHERE id = @id AND state = 'waiting';

-- EndDelivery ends a delivery that hasn't ended: cancelled or failed, with the reason.
-- name: EndDelivery :exec
UPDATE deliveries SET state = @state::text, reason = @reason, ended_at = now(), lease_until = NULL WHERE id = @id;
