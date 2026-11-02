-- ClaimDelivery claims the queued delivery of a commit to this installation's cluster that has waited longest, that no
-- controller holds and whose environment isn't applying another, until lease_until, with its environment, its target
-- and the slugs of its repository and organization.
-- name: ClaimDelivery :one
WITH claimed AS (
    SELECT d.id FROM deliveries d JOIN environments e ON e.id = d.environment_id
    WHERE d.state = 'queued' AND d.kind = 'deliver' AND coalesce(d.lease_until, d.queued_at) <= now() AND e.cluster IS NULL
      AND NOT EXISTS (SELECT 1 FROM deliveries a WHERE a.environment_id = d.environment_id AND a.state = 'applying')
    ORDER BY coalesce(d.lease_until, d.queued_at)
    LIMIT 1
    FOR UPDATE OF d SKIP LOCKED
)
UPDATE deliveries d SET lease_until = @lease_until::timestamptz
FROM claimed, environments e, delivery_targets t, repositories repo, organizations o
WHERE d.id = claimed.id AND e.id = d.environment_id AND t.principal_id = e.target_id AND repo.id = d.repo_id AND o.id = d.org_id
RETURNING d.id, d.org_id, d.repo_id, d.number, d.environment_id, d.sha::text AS sha, e.target_id, e.branch, e.namespace,
    t.name AS target, repo.slug AS repo_slug, o.slug AS org_slug;

-- DeferDelivery puts a queued delivery off until a time, with why: it waits for room. It answers its commit and its
-- target; no row when it is no longer queued.
-- name: DeferDelivery :one
UPDATE deliveries d SET lease_until = @until::timestamptz, reason = @reason
FROM environments e, delivery_targets t
WHERE d.id = @id AND d.state = 'queued' AND e.id = d.environment_id AND t.principal_id = e.target_id
RETURNING d.org_id, d.repo_id, d.sha::text AS sha, e.target_id, t.name AS target;

-- MarkDeliveryRendering marks a queued delivery rendering, as its render pod starts, and answers its commit and its
-- target; no row when it is no longer queued.
-- name: MarkDeliveryRendering :one
UPDATE deliveries d SET state = 'rendering', reason = '', started_at = now(), lease_until = NULL
FROM environments e, delivery_targets t
WHERE d.id = @id AND d.state = 'queued' AND e.id = d.environment_id AND t.principal_id = e.target_id
RETURNING d.org_id, d.repo_id, d.sha::text AS sha, e.target_id, t.name AS target;

-- InsertDeliveryToken records a delivery's token, replacing the one a controller that failed partway made.
-- name: InsertDeliveryToken :exec
INSERT INTO delivery_tokens (delivery_id, token_hash, hint, expires_at) VALUES (@delivery_id, @token_hash, @hint, @expires_at)
ON CONFLICT (delivery_id) DO UPDATE SET token_hash = excluded.token_hash, hint = excluded.hint, created_at = now(),
    expires_at = excluded.expires_at, last_used_at = NULL;

-- DeliveryTokenByHash finds the token of a delivery that is queued or rendering by its hash, with the delivery's
-- organization and repository, and the target it acts as. A render pod may use its token before the controller marks
-- the delivery rendering: the token is recorded first.
-- name: DeliveryTokenByHash :one
SELECT t.delivery_id, t.hint, t.created_at, t.expires_at, t.last_used_at, d.org_id, d.repo_id, sqlc.embed(p)
FROM delivery_tokens t JOIN deliveries d ON d.id = t.delivery_id JOIN environments e ON e.id = d.environment_id
    JOIN principals p ON p.id = e.target_id
WHERE t.token_hash = $1 AND d.state IN ('queued', 'rendering');

-- name: TouchDeliveryToken :exec
UPDATE delivery_tokens SET last_used_at = @used_at::timestamptz WHERE delivery_id = @delivery_id;

-- name: DeleteDeliveryToken :exec
DELETE FROM delivery_tokens WHERE delivery_id = $1;

-- DeleteDeadDeliveryTokens deletes the tokens of deliveries that no longer render, however they ended, and those that
-- expired.
-- name: DeleteDeadDeliveryTokens :execrows
DELETE FROM delivery_tokens t USING deliveries d
WHERE d.id = t.delivery_id AND (d.state NOT IN ('queued', 'rendering') OR t.expires_at <= now());

-- InsertDeliveryRender keeps a delivery's render, once, while it renders: no row when it doesn't, or has one. A
-- delivery still queued renders too, as its token does: its pod may upload before the controller marks it rendering.
-- name: InsertDeliveryRender :execrows
INSERT INTO delivery_renders (delivery_id, manifests)
SELECT d.id, @manifests::bytea FROM deliveries d WHERE d.id = @delivery_id AND d.state IN ('queued', 'rendering')
ON CONFLICT (delivery_id) DO NOTHING;

-- DeleteOldRenders deletes the renders of each environment's deliveries past its newest @keep, but those of deliveries
-- that haven't ended and the environment's last to succeed, which a resync applies again.
-- name: DeleteOldRenders :execrows
DELETE FROM delivery_renders r
USING (
    SELECT d.id, d.environment_id, d.state, row_number() OVER (PARTITION BY d.environment_id ORDER BY d.number DESC) AS newest
    FROM deliveries d JOIN delivery_renders kept ON kept.delivery_id = d.id
) old
WHERE r.delivery_id = old.id AND old.newest > @keep::int AND old.state IN ('succeeded', 'failed', 'cancelled')
  AND old.id IS DISTINCT FROM (SELECT s.id FROM deliveries s WHERE s.environment_id = old.environment_id
      AND s.state = 'succeeded' ORDER BY s.number DESC LIMIT 1);

-- WorkingDeliveries lists the deliveries the controller follows: those that render, and those whose apply a
-- controller left, its lease lapsed, with whether each has its render.
-- name: WorkingDeliveries :many
SELECT d.id, d.org_id, d.repo_id, d.number, d.state, coalesce(d.started_at, d.queued_at)::timestamptz AS started_at,
    EXISTS (SELECT 1 FROM delivery_renders r WHERE r.delivery_id = d.id) AS rendered
FROM deliveries d
WHERE d.state = 'rendering' OR (d.state = 'applying' AND coalesce(d.lease_until, d.queued_at) <= now())
ORDER BY d.started_at;

-- TakeDeliveryApply marks a delivery applying for a controller until lease_until: one that rendered, or one whose
-- apply another controller left. It answers what the apply needs; no row when it neither rendered nor was left.
-- name: TakeDeliveryApply :one
UPDATE deliveries d SET state = 'applying', lease_until = @lease_until::timestamptz
FROM environments e, delivery_targets t
WHERE d.id = @id AND e.id = d.environment_id AND t.principal_id = e.target_id
  AND ((d.state = 'rendering' AND EXISTS (SELECT 1 FROM delivery_renders r WHERE r.delivery_id = d.id))
    OR (d.state = 'applying' AND coalesce(d.lease_until, d.queued_at) <= now()))
RETURNING d.id, d.org_id, d.repo_id, d.number, d.environment_id, d.sha::text AS sha, e.namespace, e.revision,
    e.health AS environment_health, e.target_id, t.name AS target;

-- name: DeliveryRender :one
SELECT manifests FROM delivery_renders WHERE delivery_id = $1;

-- FinishDelivery ends a delivery that hasn't ended, as it ended, and answers its environment, its commit and its
-- target; no row when it had ended already.
-- name: FinishDelivery :one
UPDATE deliveries d SET state = @state::text, reason = @reason, ended_at = now(), lease_until = NULL
FROM environments e, delivery_targets t
WHERE d.id = @id AND d.state IN ('queued', 'rendering', 'applying') AND e.id = d.environment_id AND t.principal_id = e.target_id
RETURNING d.org_id, d.repo_id, d.number, d.environment_id, d.sha::text AS sha, e.target_id, t.name AS target;

-- DeliveriesStanding finds where each of some deliveries stands.
-- name: DeliveriesStanding :many
SELECT id, repo_id, state FROM deliveries WHERE id = ANY (@ids::uuid[]);

-- name: EnvironmentObjects :many
SELECT * FROM environment_objects WHERE environment_id = $1 ORDER BY position;

-- name: DeleteEnvironmentObjects :exec
DELETE FROM environment_objects WHERE environment_id = $1;

-- InsertEnvironmentObjects records an environment's inventory: the nth of each list is the nth object.
-- name: InsertEnvironmentObjects :exec
INSERT INTO environment_objects (environment_id, position, api_group, version, kind, namespace, name, uid, delivery_id, health, message)
SELECT @environment_id::uuid, unnest(@positions::int[]), unnest(@api_groups::text[]), unnest(@versions::text[]),
    unnest(@kinds::text[]), unnest(@namespaces::text[]), unnest(@names::text[]), unnest(@uids::text[]),
    unnest(@delivery_ids::uuid[]), unnest(@healths::text[]), unnest(@messages::text[]);

-- SetObjectHealths records the health each of an environment's objects read as, by position.
-- name: SetObjectHealths :exec
UPDATE environment_objects o SET health = h.health, message = h.message
FROM (SELECT unnest(@positions::int[]) AS position, unnest(@healths::text[]) AS health, unnest(@messages::text[]) AS message) h
WHERE o.environment_id = @environment_id AND o.position = h.position;

-- SetEnvironmentDelivered records a delivery of a commit that succeeded: the environment's revision, its health, and
-- Synced unless a newer delivery waits.
-- name: SetEnvironmentDelivered :exec
UPDATE environments e SET revision = @revision::text, synced_at = now(), health = @health::text,
    sync = CASE WHEN EXISTS (SELECT 1 FROM deliveries d WHERE d.environment_id = e.id AND d.id <> @delivery_id
        AND d.state IN ('waiting', 'queued', 'rendering', 'applying')) THEN 'out_of_sync' ELSE 'synced' END
WHERE e.id = @id;

-- SetEnvironmentHealth records an environment's health when it changed, and answers its revision and its target; no
-- row when it hadn't changed.
-- name: SetEnvironmentHealth :one
UPDATE environments e SET health = @health::text
FROM delivery_targets t
WHERE e.id = @id AND e.health <> @health::text AND t.principal_id = e.target_id
RETURNING e.org_id, e.repo_id, e.revision, e.target_id, t.name AS target;

-- ProgressingEnvironments lists the live environments in this installation's cluster whose health is Progressing, with
-- their namespaces.
-- name: ProgressingEnvironments :many
SELECT id, org_id, repo_id, namespace FROM environments
WHERE deleted_at IS NULL AND cluster IS NULL AND health = 'progressing' AND revision IS NOT NULL
ORDER BY id;
