-- LiveBranches lists a repository's live branches, by name: what people browse of its code, as pushes recorded it,
-- each with its status as a request to merge into its base, the last merge recorded of it, what a merge into it needs,
-- when /cobranch.yaml names it, the jobs its head's file lets members start by hand, and the AI reviewer's latest task
-- for its head.
-- name: LiveBranches :many
SELECT b.name, b.head, b.created_at, b.updated_at, b.base, b.ahead, b.behind, b.merges, b.status, b.checks,
    r.annotations AS required_annotations, r.linear_history, r.approvals AS required_approvals, r.ai AS required_ai, b.merged_head, b.merged_by, m.display_name AS merged_by_name, b.merged_at,
    coalesce(f.error, '')::text AS settings_error,
    (SELECT coalesce(array_agg(j ->> 'name' ORDER BY j ->> 'name'), '{}') FROM jsonb_array_elements(coalesce(f.settings -> 'jobs', '[]'::jsonb)) j
     WHERE (j -> 'on' -> 'manual') IS NOT NULL)::text[] AS manual_jobs,
    coalesce(rt.number, 0)::bigint AS review_task, coalesce(rt.state, '')::text AS review_state,
    coalesce(rt.reason, '')::text AS review_reason
FROM branches b LEFT JOIN merge_requirements r ON r.repo_id = b.repo_id AND r.branch = b.name
LEFT JOIN principals m ON m.id = b.merged_by
LEFT JOIN settings_files f ON f.repo_id = b.repo_id AND f.blob = b.settings_blob
LEFT JOIN LATERAL (SELECT t.number, t.state, t.reason FROM agent_tasks t
    WHERE t.branch_id = b.id AND t.sha = b.head AND t.kind = 'review' ORDER BY t.number DESC LIMIT 1) rt ON true
WHERE b.repo_id = $1 AND b.deleted_at IS NULL
ORDER BY b.name;

-- name: LiveBranch :one
SELECT b.name, b.head, b.created_at, b.updated_at, b.base, b.ahead, b.behind, b.merges, b.status, b.checks,
    r.annotations AS required_annotations, r.linear_history, r.approvals AS required_approvals, r.ai AS required_ai, b.merged_head, b.merged_by, m.display_name AS merged_by_name, b.merged_at,
    coalesce(f.error, '')::text AS settings_error,
    (SELECT coalesce(array_agg(j ->> 'name' ORDER BY j ->> 'name'), '{}') FROM jsonb_array_elements(coalesce(f.settings -> 'jobs', '[]'::jsonb)) j
     WHERE (j -> 'on' -> 'manual') IS NOT NULL)::text[] AS manual_jobs,
    coalesce(rt.number, 0)::bigint AS review_task, coalesce(rt.state, '')::text AS review_state,
    coalesce(rt.reason, '')::text AS review_reason
FROM branches b LEFT JOIN merge_requirements r ON r.repo_id = b.repo_id AND r.branch = b.name
LEFT JOIN principals m ON m.id = b.merged_by
LEFT JOIN settings_files f ON f.repo_id = b.repo_id AND f.blob = b.settings_blob
LEFT JOIN LATERAL (SELECT t.number, t.state, t.reason FROM agent_tasks t
    WHERE t.branch_id = b.id AND t.sha = b.head AND t.kind = 'review' ORDER BY t.number DESC LIMIT 1) rt ON true
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
-- --first-parent does, for at most max commits whose message holds grep, ignoring case (every commit's does when grep
-- is empty), nearest first, with the display names of the principals behind their authors and committers. The walk
-- stops once it found max of them.
-- name: FirstParents :many
WITH RECURSIVE line (sha, next, depth, matched, found) AS (
    SELECT s.sha, s.parents[1], 1, m.matched, m.matched::int
    FROM commits s CROSS JOIN LATERAL (SELECT strpos(lower(s.message), lower(@grep::text)) > 0 AS matched) m
    WHERE s.repo_id = @repo_id AND s.sha = @sha
    UNION ALL
    SELECT p.sha, p.parents[1], line.depth + 1, m.matched, line.found + m.matched::int
    FROM line JOIN commits p ON p.repo_id = @repo_id AND p.sha = line.next
    CROSS JOIN LATERAL (SELECT strpos(lower(p.message), lower(@grep::text)) > 0 AS matched) m
    WHERE line.found < @max::int
)
SELECT sqlc.embed(c), a.display_name AS author_display_name, m.display_name AS committer_display_name
FROM line
JOIN commits c ON c.repo_id = @repo_id AND c.sha = line.sha
LEFT JOIN principals a ON a.id = c.author_id
LEFT JOIN principals m ON m.id = c.committer_id
WHERE line.matched
ORDER BY line.depth;

-- CountFirstParents counts the commits of a repository's history from a commit along each commit's first parent whose
-- message holds grep, ignoring case (every commit's does when grep is empty), as git rev-list --first-parent --count
-- -i -F --grep does.
-- name: CountFirstParents :one
WITH RECURSIVE line (sha, next, message) AS (
    SELECT s.sha, s.parents[1], s.message FROM commits s WHERE s.repo_id = @repo_id AND s.sha = @sha
    UNION ALL
    SELECT p.sha, p.parents[1], p.message
    FROM line JOIN commits p ON p.repo_id = @repo_id AND p.sha = line.next
)
SELECT count(*)::int FROM line WHERE strpos(lower(line.message), lower(@grep::text)) > 0;

-- PushOf finds one of a repository's pushes, with its pusher's display name and the branches it moved, without
-- refs/heads/, in the order it moved them; a branch it deleted isn't one.
-- name: PushOf :one
SELECT p.id, p.pusher_id, u.display_name AS pusher_display_name, p.transport, p.committed_at,
    ARRAY(
        SELECT substr(r.u ->> 'name', length('refs/heads/') + 1)
        FROM jsonb_array_elements(p.ref_updates) WITH ORDINALITY AS r (u, n)
        WHERE starts_with(r.u ->> 'name', 'refs/heads/') AND r.u ->> 'new' <> '0000000000000000000000000000000000000000'
        ORDER BY r.n
    )::text[] AS branches
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
