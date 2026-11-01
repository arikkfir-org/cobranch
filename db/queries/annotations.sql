-- CommitOrg finds the organization of one of a repository's commits: none when the repository holds no such commit.
-- name: CommitOrg :one
SELECT org_id FROM commits WHERE repo_id = $1 AND sha = $2;

-- AnnotationsOf lists the annotations on those of a repository's commits that shas name, by commit and name, with the
-- display names of the principals who set them.
-- name: AnnotationsOf :many
SELECT sqlc.embed(a), p.display_name AS principal_display_name
FROM annotations a LEFT JOIN principals p ON p.id = a.created_by
WHERE a.repo_id = @repo_id AND a.sha = ANY (@shas::text[])
ORDER BY a.sha, a.name;

-- AnnotationForUpdate finds the annotation of a name on one of a repository's commits, with the display name of the
-- principal who set it, and locks it until the transaction ends.
-- name: AnnotationForUpdate :one
SELECT sqlc.embed(a), p.display_name AS principal_display_name
FROM annotations a LEFT JOIN principals p ON p.id = a.created_by
WHERE a.repo_id = $1 AND a.sha = $2 AND a.name = $3
FOR UPDATE OF a;

-- InsertAnnotation sets an annotation that isn't there yet, its run starting now and completed now when its status
-- ends one: none when another transaction set it first.
-- name: InsertAnnotation :one
INSERT INTO annotations (id, org_id, repo_id, sha, name, status, summary, link, created_by, completed_at)
VALUES (@id, @org_id, @repo_id, @sha, @name, @status, @summary, @link, @created_by,
        CASE WHEN @status::text IN ('failed', 'successful') THEN now() END)
ON CONFLICT (repo_id, sha, name) DO NOTHING
RETURNING *;

-- UpdateAnnotation changes an annotation: a run starts again when it goes back from failed or successful to pending or
-- in_progress, and completes when it reaches failed or successful from another status.
-- name: UpdateAnnotation :one
UPDATE annotations SET
    status = @status::text, summary = @summary, link = @link, updated_at = now(),
    started_at = CASE WHEN status IN ('failed', 'successful') AND @status::text IN ('pending', 'in_progress') THEN now() ELSE started_at END,
    completed_at = CASE
        WHEN @status::text IN ('pending', 'in_progress') THEN NULL
        WHEN status <> @status::text THEN now()
        ELSE completed_at
    END
WHERE id = @id
RETURNING *;

-- name: DeleteAnnotation :exec
DELETE FROM annotations WHERE id = $1;
