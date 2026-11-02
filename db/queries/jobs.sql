-- InsertSettingsFile records a /cobranch.yaml a push brought to a branch tip, unless a push recorded the same one.
-- name: InsertSettingsFile :batchexec
INSERT INTO settings_files (repo_id, blob, org_id, settings, error)
VALUES (@repo_id, @blob, @org_id, @settings, @error)
ON CONFLICT (repo_id, blob) DO NOTHING;

-- SetBranchSettings names the /cobranch.yaml at a live branch's head: none with a null blob.
-- name: SetBranchSettings :batchexec
UPDATE branches SET settings_blob = sqlc.narg(blob)::text WHERE repo_id = @repo_id AND name = @name AND deleted_at IS NULL;

-- BranchSettings finds a live branch's head and the /cobranch.yaml there: what it says or why it doesn't parse, both
-- null without one.
-- name: BranchSettings :one
SELECT b.head, f.settings, f.error
FROM branches b LEFT JOIN settings_files f ON f.repo_id = b.repo_id AND f.blob = b.settings_blob
WHERE b.repo_id = @repo_id AND b.name = @name AND b.deleted_at IS NULL;

-- PrincipalStanding finds what a principal is and whether they are a member of an organization.
-- name: PrincipalStanding :one
SELECT p.kind, EXISTS (SELECT 1 FROM memberships m WHERE m.org_id = @org_id AND m.user_id = p.id)::boolean AS member
FROM principals p WHERE p.id = @id;

-- RepositoryRolePermissions finds the permissions of a built-in repository role by name.
-- name: RepositoryRolePermissions :one
SELECT permissions FROM roles WHERE scope = 'repository' AND name = @name AND built_in;

-- name: JobPrincipal :one
SELECT principal_id FROM jobs WHERE repo_id = @repo_id AND name = @name;

-- InsertJob makes a job of a repository a principal of its organization.
-- name: InsertJob :exec
WITH principal AS (
    INSERT INTO principals (id, kind, display_name, created_at, org_id) VALUES (@id, 'job', @name, now(), @org_id)
)
INSERT INTO jobs (principal_id, org_id, repo_id, name) VALUES (@id, @org_id, @repo_id, @name);

-- LockRunQueue locks the repository's run counter, making it first, to the end of the transaction: runs are queued for
-- one repository at a time, so two changes that reach a commit at once can't both find no run of a job there.
-- name: LockRunQueue :exec
INSERT INTO run_numbers (repo_id, last) VALUES (@repo_id, 0)
ON CONFLICT (repo_id) DO UPDATE SET last = run_numbers.last;

-- NextRunNumber counts a repository's runs on by one.
-- name: NextRunNumber :one
INSERT INTO run_numbers (repo_id, last) VALUES (@repo_id, 1)
ON CONFLICT (repo_id) DO UPDATE SET last = run_numbers.last + 1
RETURNING last;

-- SupersedeRuns cancels a job's runs that haven't ended on a ref at other commits than one, and answers the commit of
-- each.
-- name: SupersedeRuns :many
UPDATE job_runs SET state = 'cancelled', reason = @reason, ended_at = now(), lease_until = NULL
WHERE job_id = @job_id AND state IN ('waiting', 'queued', 'running') AND ref = @ref AND sha <> @sha
RETURNING id, sha;

-- ActiveRunAt finds a job's newest run at a commit that hasn't ended, locked until the transaction ends.
-- name: ActiveRunAt :one
SELECT id, state FROM job_runs WHERE job_id = @job_id AND sha = @sha AND state IN ('waiting', 'queued', 'running')
ORDER BY number DESC LIMIT 1
FOR UPDATE;

-- name: InsertRun :exec
INSERT INTO job_runs (id, org_id, repo_id, number, job_id, sha, ref, trigger, cause_run_id, definition, state, reason, ended_at)
VALUES (@id, @org_id, @repo_id, @number, @job_id, @sha, @ref, @trigger, @cause_run_id, @definition, @state, @reason,
        CASE WHEN @state::text IN ('succeeded', 'failed', 'cancelled') THEN now() END);

-- RunsOf lists a page of a repository's runs, newest first, from before a number (0 for the newest), at a commit when
-- one is named, with their jobs' names and the names of who started them.
-- name: RunsOf :many
SELECT sqlc.embed(r), j.name AS job, p.display_name AS started_by_name
FROM job_runs r JOIN jobs j ON j.principal_id = r.job_id LEFT JOIN principals p ON p.id = r.started_by
WHERE r.repo_id = @repo_id AND (@before::bigint = 0 OR r.number < @before::bigint)
  AND (@sha::text = '' OR r.sha = @sha::text)
ORDER BY r.number DESC
LIMIT @lim;

-- name: RunByNumber :one
SELECT sqlc.embed(r), j.name AS job, p.display_name AS started_by_name
FROM job_runs r JOIN jobs j ON j.principal_id = r.job_id LEFT JOIN principals p ON p.id = r.started_by
WHERE r.repo_id = @repo_id AND r.number = @number;

-- RunForUpdate finds a run by number with its job's name, and locks it until the transaction ends.
-- name: RunForUpdate :one
SELECT sqlc.embed(r), j.name AS job
FROM job_runs r JOIN jobs j ON j.principal_id = r.job_id
WHERE r.repo_id = @repo_id AND r.number = @number
FOR UPDATE OF r;

-- EndRun ends a run that hasn't ended: cancelled or failed, with the reason.
-- name: EndRun :exec
UPDATE job_runs SET state = @state::text, reason = @reason, ended_at = now(), lease_until = NULL WHERE id = @id;

-- StartWaitingRun queues a waiting run for whoever started it.
-- name: StartWaitingRun :exec
UPDATE job_runs SET state = 'queued', reason = '', started_by = @started_by, queued_at = now() WHERE id = @id AND state = 'waiting';

-- name: PrincipalName :one
SELECT display_name FROM principals WHERE id = $1;

-- name: RunNumber :one
SELECT number FROM job_runs WHERE id = $1;
