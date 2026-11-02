-- ClaimTask claims the queued task that has waited longest and that no controller holds, until lease_until, with what
-- its pod starts from: its organization's slug, its agent's model and secrets, and the secret that holds the key of its
-- agent's provider.
-- name: ClaimTask :one
WITH claimed AS (
    SELECT t.id FROM agent_tasks t LEFT JOIN agent_task_pods p ON p.task_id = t.id
    WHERE t.state = 'queued' AND coalesce(p.lease_until, t.created_at) <= now()
    ORDER BY coalesce(p.lease_until, t.created_at)
    LIMIT 1
    FOR UPDATE OF t SKIP LOCKED
), leased AS (
    INSERT INTO agent_task_pods (task_id, lease_until) SELECT id, @lease_until::timestamptz FROM claimed
    ON CONFLICT (task_id) DO UPDATE SET lease_until = excluded.lease_until
    RETURNING task_id, attempt
)
SELECT t.id, t.org_id, t.number, t.agent_id, o.slug AS org_slug, a.provider, a.model, a.secrets, mp.secret_id AS key_id,
    leased.attempt
FROM leased JOIN agent_tasks t ON t.id = leased.task_id JOIN agents a ON a.principal_id = t.agent_id
JOIN organizations o ON o.id = t.org_id LEFT JOIN model_providers mp ON mp.org_id = t.org_id AND mp.provider = a.provider;

-- TaskToStart finds a task that runs or waits, with what its pod starts from, as ClaimTask answers it: the controller
-- starts its pod again.
-- name: TaskToStart :one
SELECT t.id, t.org_id, t.number, t.agent_id, o.slug AS org_slug, a.provider, a.model, a.secrets, mp.secret_id AS key_id,
    p.attempt
FROM agent_tasks t JOIN agent_task_pods p ON p.task_id = t.id JOIN agents a ON a.principal_id = t.agent_id
JOIN organizations o ON o.id = t.org_id LEFT JOIN model_providers mp ON mp.org_id = t.org_id AND mp.provider = a.provider
WHERE t.id = $1 AND t.state IN ('running', 'waiting');

-- DeferTask puts a queued task off until a time: it waits for room in its organization's quota.
-- name: DeferTask :exec
UPDATE agent_task_pods SET lease_until = @until::timestamptz WHERE task_id = @task_id;

-- MarkTaskRunning marks a queued task running, as its first pod starts, and answers its organization, number and
-- agent; no row when it is no longer queued.
-- name: MarkTaskRunning :one
UPDATE agent_tasks SET state = 'running', started_at = now() WHERE id = $1 AND state = 'queued'
RETURNING org_id, number, agent_id;

-- RecordTaskPod records that a task's pod numbered attempt started now.
-- name: RecordTaskPod :exec
UPDATE agent_task_pods SET attempt = @attempt, pod_started_at = now(), lease_until = NULL WHERE task_id = @task_id;

-- FailTask ends a task that hasn't ended Failed, for reason, and answers its organization, number and agent; no row
-- when it had ended already.
-- name: FailTask :one
UPDATE agent_tasks SET state = 'failed', reason = @reason, summary = '', ended_at = now()
WHERE id = @id AND state IN ('queued', 'running', 'waiting')
RETURNING org_id, number, agent_id;

-- ActiveTasks lists the tasks that run or wait in the pods the controller started, with their newest pod and when
-- their newest token was issued.
-- name: ActiveTasks :many
SELECT t.id, t.org_id, t.number, t.state, coalesce(t.started_at, t.created_at)::timestamptz AS started_at, p.attempt,
    coalesce(p.pod_started_at, t.created_at)::timestamptz AS pod_started_at,
    coalesce((SELECT max(k.created_at) FROM agent_task_tokens k WHERE k.task_id = t.id), 'epoch')::timestamptz AS token_at
FROM agent_tasks t JOIN agent_task_pods p ON p.task_id = t.id
WHERE t.state IN ('running', 'waiting');

-- TaskStates answers the states of those of ids that are tasks.
-- name: TaskStates :many
SELECT id, state FROM agent_tasks WHERE id = ANY(@ids::uuid[]);
