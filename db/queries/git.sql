-- Snapshot reads a repository's refs and its active packs in one statement, so a fetch never sees a ref without the
-- pack that holds its objects.
-- name: Snapshot :one
SELECT
    coalesce((SELECT array_agg(f.name ORDER BY f.name) FROM refs f WHERE f.repo_id = $1), '{}')::text[] AS ref_names,
    coalesce((SELECT array_agg(f.sha ORDER BY f.name) FROM refs f WHERE f.repo_id = $1), '{}')::text[] AS ref_shas,
    coalesce((SELECT array_agg(p.name ORDER BY p.created_at, p.name) FROM packs p WHERE p.repo_id = $1 AND p.state = 'active'), '{}')::text[] AS packs,
    coalesce((SELECT array_agg(p.upload ORDER BY p.created_at, p.name) FROM packs p WHERE p.repo_id = $1 AND p.state = 'active'), '{}')::uuid[] AS pack_uploads;

-- LockRepository locks a repository's row for a push, so pushes into one repository apply one at a time.
-- name: LockRepository :one
SELECT org_id FROM repositories WHERE id = $1 FOR UPDATE;

-- name: PushRecorded :one
SELECT EXISTS (SELECT 1 FROM pushes WHERE id = $1);

-- name: RefsOf :many
SELECT name, sha FROM refs WHERE repo_id = $1;

-- InsertPack lists a pack. A pack the repository lists already, which a push brought again, keeps its upload while
-- active; one compaction superseded turns active again, with this upload's files, as maintenance may be deleting the
-- old upload's.
-- name: InsertPack :exec
INSERT INTO packs (repo_id, name, org_id, upload, size, objects) VALUES (@repo_id, @name, @org_id, @upload, @size, @objects)
ON CONFLICT (repo_id, name) DO UPDATE SET upload = EXCLUDED.upload, state = 'active', superseded_at = NULL
WHERE packs.state <> 'active';

-- name: InsertPush :exec
INSERT INTO pushes (id, org_id, repo_id, pusher_id, transport, ref_updates, pack, received_at, traceparent)
VALUES (@id, @org_id, @repo_id, @pusher_id, @transport, @ref_updates, @pack, @received_at, @traceparent);

-- InsertCommit records a commit a push brought, unless another push brought it first; the push worker fills in its
-- author's and committer's principals.
-- name: InsertCommit :batchexec
INSERT INTO commits (repo_id, sha, org_id, tree, parents, message, author_name, author_email, authored_at, author_zone,
    committer_name, committer_email, committed_at, committer_zone, signature, first_push)
VALUES (@repo_id, @sha, @org_id, @tree, @parents, @message, @author_name, @author_email, @authored_at, @author_zone,
    @committer_name, @committer_email, @committed_at, @committer_zone, @signature, @first_push)
ON CONFLICT (repo_id, sha) DO NOTHING;

-- EmailPrincipal finds who holds an email, whether verified, and whether they sign in: a principal with no identity is
-- a login-less user.
-- name: EmailPrincipal :one
SELECT e.principal_id, e.verified, EXISTS (SELECT 1 FROM identities i WHERE i.principal_id = e.principal_id) AS signs_in
FROM principal_emails e WHERE e.email = $1;

-- name: SetRef :exec
INSERT INTO refs (repo_id, name, org_id, sha) VALUES (@repo_id, @name, @org_id, @sha)
ON CONFLICT (repo_id, name) DO UPDATE SET sha = EXCLUDED.sha;

-- name: DeleteRef :exec
DELETE FROM refs WHERE repo_id = $1 AND name = $2;

-- name: InsertBranch :exec
INSERT INTO branches (id, org_id, repo_id, name, head, created_at, updated_at)
VALUES (@id, @org_id, @repo_id, @name, @head, @at, @at);

-- name: MoveBranch :execrows
UPDATE branches SET head = @head, updated_at = @at WHERE repo_id = @repo_id AND name = @name AND deleted_at IS NULL;

-- name: DeleteBranch :execrows
UPDATE branches SET deleted_at = @at::timestamptz, updated_at = @at::timestamptz WHERE repo_id = @repo_id AND name = @name AND deleted_at IS NULL;

-- NotifyPush wakes the push workers that listen, once the push's transaction commits.
-- name: NotifyPush :exec
SELECT pg_notify('cobranch_pushes', @push_id::text);

-- ListenPushes has the connection hear NotifyPush's notifications.
-- name: ListenPushes :exec
LISTEN cobranch_pushes;

-- ClaimPush locks the push that has waited longest for the push worker and may be tried now, skipping those another
-- push worker holds.
-- name: ClaimPush :one
SELECT id, repo_id, received_at, attempts, traceparent FROM pushes
WHERE processed_at IS NULL AND retry_at <= now()
ORDER BY retry_at, committed_at
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- name: PushCommits :many
SELECT sha, author_name, author_email, committer_name, committer_email FROM commits WHERE first_push = $1;

-- name: SetCommitPrincipals :batchexec
UPDATE commits SET author_id = @author_id, committer_id = @committer_id WHERE repo_id = @repo_id AND sha = @sha;

-- name: MarkPushProcessed :exec
UPDATE pushes SET processed_at = now(), error = '' WHERE id = $1;

-- FailPush keeps a push the push worker failed waiting, for another try after delay seconds, unless another push worker
-- processed it once the failed transaction let go of it.
-- name: FailPush :exec
UPDATE pushes SET attempts = attempts + 1, error = @message, retry_at = now() + make_interval(secs => @delay::float8)
WHERE id = @id AND processed_at IS NULL;

-- CrowdedRepositories lists the repositories that have at least threshold active packs, for compaction.
-- name: CrowdedRepositories :many
SELECT repo_id FROM packs WHERE state = 'active' GROUP BY repo_id HAVING count(*) >= @threshold::bigint ORDER BY repo_id;

-- SupersedePacks marks the repository's active packs named superseded, once compaction listed the pack that holds
-- their objects.
-- name: SupersedePacks :execrows
UPDATE packs SET state = 'superseded', superseded_at = now()
WHERE repo_id = @repo_id AND state = 'active' AND name = ANY(@names::text[]);

-- SupersededPacks lists the packs superseded before the time, whose files maintenance deletes.
-- name: SupersededPacks :many
SELECT repo_id, name, upload FROM packs WHERE state = 'superseded' AND superseded_at < @before::timestamptz ORDER BY superseded_at;

-- ForgetPack deletes a superseded pack's row, once its files are gone, unless a push made it active again.
-- name: ForgetPack :exec
DELETE FROM packs WHERE repo_id = @repo_id AND name = @name AND upload = @upload AND state = 'superseded';

-- PackUploads lists every upload a pack's row names, whatever its state.
-- name: PackUploads :many
SELECT upload FROM packs;
