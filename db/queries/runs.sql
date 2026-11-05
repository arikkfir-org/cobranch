-- RunTokenByHash finds the token of a run that hasn't ended by its hash, with the run's organization, repository and
-- what its job declared it may do, and the job it acts as. A run's pod may use its token before the controller marks
-- the run running: the token is recorded first.
-- name: RunTokenByHash :one
SELECT t.run_id, t.hint, t.created_at, t.expires_at, t.last_used_at, r.org_id, r.repo_id,
       coalesce(r.definition -> 'permissions', '[]'::jsonb)::jsonb AS permissions, sqlc.embed(p)
FROM run_tokens t JOIN job_runs r ON r.id = t.run_id JOIN principals p ON p.id = r.job_id
WHERE t.token_hash = $1 AND r.state IN ('queued', 'running');

-- name: TouchRunToken :exec
UPDATE run_tokens SET last_used_at = @used_at::timestamptz WHERE run_id = @run_id;

-- ClaimRun claims the queued run of a pod or a script that has waited longest and that no controller holds, until
-- lease_until, with its job's name and its repository's and organization's slugs.
-- name: ClaimRun :one
WITH claimed AS (
    SELECT r.id FROM job_runs r
    WHERE r.state = 'queued' AND coalesce(r.lease_until, r.queued_at) <= now()
    ORDER BY coalesce(r.lease_until, r.queued_at)
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE job_runs r SET lease_until = @lease_until::timestamptz
FROM claimed, jobs j, repositories repo, organizations o
WHERE r.id = claimed.id AND j.principal_id = r.job_id AND repo.id = r.repo_id AND o.id = r.org_id
RETURNING r.id, r.org_id, r.repo_id, r.number, r.job_id, r.sha, r.ref, r.trigger, r.definition, r.queued_at,
    j.name AS job, repo.slug AS repo_slug, o.slug AS org_slug;

-- DeferRun puts a queued run off until a time, with why: it waits for room. It answers the run's commit and its job's
-- name; no row when the run is no longer queued.
-- name: DeferRun :one
UPDATE job_runs r SET lease_until = @until::timestamptz, reason = @reason
FROM jobs j
WHERE r.id = @id AND r.state = 'queued' AND j.principal_id = r.job_id
RETURNING r.id, r.org_id, r.repo_id, r.job_id, r.sha, j.name AS job;

-- MarkRunRunning marks a queued run running, as its pod starts, and answers its commit and its job's name; no row when
-- the run is no longer queued.
-- name: MarkRunRunning :one
UPDATE job_runs r SET state = 'running', reason = '', started_at = now(), lease_until = NULL
FROM jobs j
WHERE r.id = @id AND r.state = 'queued' AND j.principal_id = r.job_id
RETURNING r.id, r.org_id, r.repo_id, r.job_id, r.sha, j.name AS job;

-- InsertRunToken records a run's token, replacing the one a controller that failed partway made.
-- name: InsertRunToken :exec
INSERT INTO run_tokens (run_id, token_hash, hint, expires_at) VALUES (@run_id, @token_hash, @hint, @expires_at)
ON CONFLICT (run_id) DO UPDATE SET token_hash = excluded.token_hash, hint = excluded.hint, created_at = now(),
    expires_at = excluded.expires_at, last_used_at = NULL;

-- FinishRun ends a run that hasn't ended, as it ended, and answers its commit and its job's name; no row when it had
-- ended already.
-- name: FinishRun :one
UPDATE job_runs r SET state = @state::text, reason = @reason, ended_at = now(), lease_until = NULL
FROM jobs j
WHERE r.id = @id AND r.state IN ('queued', 'running') AND j.principal_id = r.job_id
RETURNING r.id, r.org_id, r.repo_id, r.job_id, r.sha, j.name AS job;

-- FinishReportedRuns ends the webhook runs of a job at a commit that haven't ended as their receiver reported through
-- the job's own annotation, Successful or Failed, and answers them. A queued one ends too: the controller records its
-- token before it posts it, so its receiver may report before the controller sees the 2xx and marks it running.
-- name: FinishReportedRuns :many
UPDATE job_runs r SET state = @state::text, reason = @reason, started_at = coalesce(r.started_at, now()), ended_at = now(),
    lease_until = NULL
FROM jobs j
WHERE r.job_id = @job_id AND r.repo_id = @repo_id AND r.sha = @sha AND r.state IN ('queued', 'running')
    AND r.definition -> 'webhook' IS NOT NULL AND j.principal_id = r.job_id AND j.name = @name
RETURNING r.id;

-- DeleteRunToken deletes a run's token: the run ended.
-- name: DeleteRunToken :exec
DELETE FROM run_tokens WHERE run_id = $1;

-- DeleteDeadRunTokens deletes the tokens of runs that ended another way than the controller ending them (a member
-- cancelled them, or a newer run superseded them), and those that expired.
-- name: DeleteDeadRunTokens :execrows
DELETE FROM run_tokens t USING job_runs r
WHERE r.id = t.run_id AND (r.state NOT IN ('queued', 'running') OR t.expires_at <= now());

-- RunningRuns lists the runs of pods and scripts that run, with what the controller follows each by.
-- name: RunningRuns :many
SELECT id, org_id, repo_id, number, started_at, definition FROM job_runs
WHERE state = 'running' AND definition -> 'webhook' IS NULL
ORDER BY started_at;

-- RunningWebhookRuns lists the webhook runs that run, waiting for their receivers to report.
-- name: RunningWebhookRuns :many
SELECT id, org_id, repo_id, number, started_at, definition FROM job_runs
WHERE state = 'running' AND definition -> 'webhook' IS NOT NULL
ORDER BY started_at;

-- RunsStanding finds those of ids that are runs, with where each stands and its repository.
-- name: RunsStanding :many
SELECT id, repo_id, state, ended_at FROM job_runs WHERE id = ANY(@ids::uuid[]);

-- OrganizationsAmong answers which of ids are organizations still.
-- name: OrganizationsAmong :many
SELECT id FROM organizations WHERE id = ANY(@ids::uuid[]);

-- name: DeleteRunSteps :exec
DELETE FROM job_run_steps WHERE run_id = $1;

-- InsertRunStep keeps one of a run's steps, at its position.
-- name: InsertRunStep :batchexec
INSERT INTO job_run_steps (run_id, position, name, state, exit_code, reason, started_at, ended_at)
VALUES (@run_id, @position, @name, @state, @exit_code, @reason, @started_at, @ended_at);
