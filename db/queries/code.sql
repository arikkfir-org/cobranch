-- LiveBranches lists a repository's live branches, by name: what people browse of its code, as pushes recorded it,
-- each with its status as a request to merge into its base, the last merge recorded of it, and what a merge into it
-- needs, when /cobranch.yaml names it.
-- name: LiveBranches :many
SELECT b.name, b.head, b.created_at, b.updated_at, b.base, b.ahead, b.behind, b.merges, b.status, b.checks,
    r.annotations AS required_annotations, r.linear_history, r.approvals AS required_approvals, b.merged_head, b.merged_by, m.display_name AS merged_by_name, b.merged_at
FROM branches b LEFT JOIN merge_requirements r ON r.repo_id = b.repo_id AND r.branch = b.name
LEFT JOIN principals m ON m.id = b.merged_by
WHERE b.repo_id = $1 AND b.deleted_at IS NULL
ORDER BY b.name;

-- name: LiveBranch :one
SELECT b.name, b.head, b.created_at, b.updated_at, b.base, b.ahead, b.behind, b.merges, b.status, b.checks,
    r.annotations AS required_annotations, r.linear_history, r.approvals AS required_approvals, b.merged_head, b.merged_by, m.display_name AS merged_by_name, b.merged_at
FROM branches b LEFT JOIN merge_requirements r ON r.repo_id = b.repo_id AND r.branch = b.name
LEFT JOIN principals m ON m.id = b.merged_by
WHERE b.repo_id = $1 AND b.name = $2 AND b.deleted_at IS NULL;

-- DeletedBranch reads the latest deletion of a repository's branch of a name, with the display names of the
-- principals who merged and deleted it; none while a branch of that name is live.
-- name: DeletedBranch :one
SELECT b.id, b.name, b.head, b.created_at, b.deleted_at, b.deleted_by, d.display_name AS deleted_by_name,
    b.merged_head, b.merged_by, m.display_name AS merged_by_name, b.merged_at
FROM branches b
LEFT JOIN principals d ON d.id = b.deleted_by
LEFT JOIN principals m ON m.id = b.merged_by
WHERE b.repo_id = @repo_id AND b.name = @name AND b.deleted_at IS NOT NULL
    AND NOT EXISTS (SELECT 1 FROM branches l WHERE l.repo_id = @repo_id AND l.name = @name AND l.deleted_at IS NULL)
ORDER BY b.deleted_at DESC
LIMIT 1;

-- CommitsAt finds those of a repository's commits that shas name, with the display names of the principals behind
-- their authors and committers.
-- name: CommitsAt :many
SELECT sqlc.embed(c), a.display_name AS author_display_name, m.display_name AS committer_display_name
FROM commits c
LEFT JOIN principals a ON a.id = c.author_id
LEFT JOIN principals m ON m.id = c.committer_id
WHERE c.repo_id = @repo_id AND c.sha = ANY (@shas::text[]);

-- FirstParents walks a repository's history from a commit along each commit's first parent, as git log
-- --first-parent does, for at most max commits, nearest first, with the display names of the principals behind
-- their authors and committers.
-- name: FirstParents :many
WITH RECURSIVE line (sha, next, depth) AS (
    SELECT s.sha, s.parents[1], 1 FROM commits s WHERE s.repo_id = @repo_id AND s.sha = @sha
    UNION ALL
    SELECT p.sha, p.parents[1], line.depth + 1
    FROM line JOIN commits p ON p.repo_id = @repo_id AND p.sha = line.next
    WHERE line.depth < @max::int
)
SELECT sqlc.embed(c), a.display_name AS author_display_name, m.display_name AS committer_display_name
FROM line
JOIN commits c ON c.repo_id = @repo_id AND c.sha = line.sha
LEFT JOIN principals a ON a.id = c.author_id
LEFT JOIN principals m ON m.id = c.committer_id
ORDER BY line.depth;

-- PushOf finds one of a repository's pushes, with its pusher's display name.
-- name: PushOf :one
SELECT p.id, p.pusher_id, u.display_name AS pusher_display_name, p.transport, p.committed_at
FROM pushes p LEFT JOIN principals u ON u.id = p.pusher_id
WHERE p.repo_id = $1 AND p.id = $2;

-- Tags lists a repository's tags, by name, each with the object its ref names: a commit, or an annotated tag.
-- name: Tags :many
SELECT substr(name, length('refs/tags/') + 1)::text AS name, sha FROM refs
WHERE repo_id = $1 AND starts_with(name, 'refs/tags/')
ORDER BY name COLLATE "C";

-- Slugs finds a repository's slug and its organization's, which its rendered Markdown's links name.
-- name: Slugs :one
SELECT o.slug AS org, r.slug AS repo FROM repositories r JOIN organizations o ON o.id = r.org_id WHERE r.id = $1;
