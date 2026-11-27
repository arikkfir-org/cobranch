-- InsertTaskToken records a token of the task while it hasn't ended: no row for a task that isn't there or ended.
-- name: InsertTaskToken :execrows
INSERT INTO agent_task_tokens (token_hash, task_id, expires_at)
SELECT @token_hash::bytea, t.id, @expires_at::timestamptz FROM agent_tasks t
WHERE t.id = @task_id AND t.state IN ('queued', 'running', 'waiting');

-- TaskToken finds a token of a task that hasn't ended, with the task's organization, its assigner and whether they
-- are suspended, whether it is private and the thread it talks in, whether it is a job's run's, the repository and
-- permissions of that run while it runs, and its agent.
-- name: TaskToken :one
SELECT k.task_id, k.created_at, k.expires_at, t.org_id, t.assigned_by, a.suspended AS assigner_suspended, t.private, t.thread_id,
    (t.kind = 'job')::boolean AS of_job, r.repo_id AS run_repo_id, (r.definition -> 'permissions')::jsonb AS run_permissions, sqlc.embed(p)
FROM agent_task_tokens k JOIN agent_tasks t ON t.id = k.task_id JOIN principals p ON p.id = t.agent_id
LEFT JOIN principals a ON a.id = t.assigned_by
LEFT JOIN job_runs r ON r.id = t.run_id AND r.state = 'running'
WHERE k.token_hash = @token_hash AND t.state IN ('queued', 'running', 'waiting');

-- DeleteExpiredTaskTokens deletes the tokens that expired before now, and those of tasks that ended.
-- name: DeleteExpiredTaskTokens :execrows
DELETE FROM agent_task_tokens k USING agent_tasks t
WHERE t.id = k.task_id AND (k.expires_at < @now::timestamptz OR t.state NOT IN ('queued', 'running', 'waiting'));
