-- Snapshot reads a repository's refs and its active packs in one statement, so a fetch never sees a ref without the
-- pack that holds its objects.
-- name: Snapshot :one
SELECT
    coalesce((SELECT array_agg(f.name ORDER BY f.name) FROM refs f WHERE f.repo_id = $1), '{}')::text[] AS ref_names,
    coalesce((SELECT array_agg(f.sha ORDER BY f.name) FROM refs f WHERE f.repo_id = $1), '{}')::text[] AS ref_shas,
    coalesce((SELECT array_agg(p.name ORDER BY p.created_at, p.name) FROM packs p WHERE p.repo_id = $1 AND p.state = 'active'), '{}')::text[] AS packs;

-- LockRepository locks a repository's row for a push, so pushes into one repository apply one at a time.
-- name: LockRepository :one
SELECT org_id FROM repositories WHERE id = $1 FOR UPDATE;

-- name: PushRecorded :one
SELECT EXISTS (SELECT 1 FROM pushes WHERE id = $1);

-- name: RefsOf :many
SELECT name, sha FROM refs WHERE repo_id = $1;

-- name: InsertPack :exec
INSERT INTO packs (repo_id, name, org_id, size, objects) VALUES (@repo_id, @name, @org_id, @size, @objects);

-- name: InsertPush :exec
INSERT INTO pushes (id, org_id, repo_id, pusher_id, transport, ref_updates, pack, received_at)
VALUES (@id, @org_id, @repo_id, @pusher_id, @transport, @ref_updates, @pack, @received_at);

-- InsertCommit records a commit a push brought, unless another push brought it first.
-- name: InsertCommit :batchexec
INSERT INTO commits (repo_id, sha, org_id, tree, parents, message, author_id, author_name, author_email, authored_at,
    author_zone, committer_id, committer_name, committer_email, committed_at, committer_zone, signature, first_push)
VALUES (@repo_id, @sha, @org_id, @tree, @parents, @message, @author_id, @author_name, @author_email, @authored_at,
    @author_zone, @committer_id, @committer_name, @committer_email, @committed_at, @committer_zone, @signature, @first_push)
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
