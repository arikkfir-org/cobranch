-- ViewedBranch finds the IDs of the repository's live branch of name and of its organization.
-- name: ViewedBranch :one
SELECT id, org_id FROM branches WHERE repo_id = @repo_id AND name = @name AND deleted_at IS NULL;

-- LockViewed holds the Viewed marks that key names, a principal's on a branch, until the transaction ends, so that a
-- change reads them, compares them with those its caller read and writes them as one.
-- name: LockViewed :exec
SELECT pg_advisory_xact_lock(hashtextextended(@key::text, 0));

-- ViewedFiles lists a principal's Viewed marks on a branch, by path.
-- name: ViewedFiles :many
SELECT path, object, viewed_at FROM viewed_files WHERE principal_id = @principal_id AND branch_id = @branch_id ORDER BY path;

-- CountViewed counts a principal's Viewed marks on a branch.
-- name: CountViewed :one
SELECT count(*) FROM viewed_files WHERE principal_id = @principal_id AND branch_id = @branch_id;

-- MarkViewed marks a principal's file of a branch Viewed at object, or moves its mark there; a mark already at object
-- stays as it was, with the moment it was made.
-- name: MarkViewed :exec
INSERT INTO viewed_files (org_id, principal_id, branch_id, path, object, viewed_at)
VALUES (@org_id, @principal_id, @branch_id, @path, @object, @viewed_at)
ON CONFLICT (principal_id, branch_id, path) DO UPDATE SET object = excluded.object, viewed_at = excluded.viewed_at
WHERE viewed_files.object <> excluded.object;

-- UnmarkViewed takes a principal's Viewed mark off a file of a branch, if it has one.
-- name: UnmarkViewed :exec
DELETE FROM viewed_files WHERE principal_id = @principal_id AND branch_id = @branch_id AND path = @path;
