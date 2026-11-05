-- OrganizationActivity lists, newest first, at most lim of what happened in the organization's repositories of
-- repo_ids before the point before_at and before_id name (from the newest without one): its events of pushes, with
-- the refs they moved and the branches they merged into main, of reviews, with the live branch whose head they
-- reviewed, and of repositories created; and its deliveries that ended, succeeded or failed, but those that applied
-- a render again, as their environment's target, with their environment's branch. Each with who acted and the
-- repository.
-- name: OrganizationActivity :many
SELECT a.id, a.at::timestamptz AS at, a.kind::text AS kind, a.actor_id, coalesce(p.display_name, '')::text AS actor_name,
    coalesce(p.kind, '')::text AS actor_kind, r.id AS repo_id, r.slug AS repo_slug,
    coalesce(pu.ref_updates, '[]'::jsonb) AS ref_updates, coalesce(pu.merged, '[]'::jsonb) AS merged, a.commits::int AS commits,
    coalesce(a.sha, '')::text AS sha, a.review_state::text AS review_state, coalesce(rb.name, '')::text AS review_branch,
    a.delivery_number::bigint AS delivery_number, a.delivery_kind::text AS delivery_kind,
    a.delivery_state::text AS delivery_state, coalesce(env.branch, '')::text AS environment
FROM (
    SELECT e.id, e.at, e.kind, e.actor_id,
        CASE WHEN e.kind = 'review.submitted' THEN (e.details ->> 'repo')::uuid ELSE e.subject_id END AS repo_id,
        CASE WHEN e.kind = 'repo.pushed' THEN (e.details ->> 'push')::uuid END AS push_id,
        coalesce((e.details ->> 'commits')::int, 0) AS commits, e.details ->> 'sha' AS sha, coalesce(e.details ->> 'state', '') AS review_state,
        0::bigint AS delivery_number, '' AS delivery_kind, '' AS delivery_state, NULL::uuid AS environment_id
    FROM events e
    WHERE e.org_id = @org_id AND e.kind IN ('repo.pushed', 'repo.created', 'review.submitted')
    UNION ALL
    SELECT d.id, d.ended_at, 'delivery.ended', t.target_id, d.repo_id, NULL, 0, d.sha, '', d.number, d.kind, d.state, d.environment_id
    FROM deliveries d JOIN environments t ON t.id = d.environment_id
    WHERE d.org_id = @org_id AND d.ended_at IS NOT NULL AND d.kind <> 'resync' AND d.state IN ('succeeded', 'failed')
) a
JOIN repositories r ON r.id = a.repo_id
LEFT JOIN principals p ON p.id = a.actor_id
LEFT JOIN pushes pu ON pu.id = a.push_id
LEFT JOIN environments env ON env.id = a.environment_id
LEFT JOIN LATERAL (SELECT b.name FROM branches b
    WHERE a.kind = 'review.submitted' AND b.repo_id = r.id AND b.head = a.sha AND b.deleted_at IS NULL
    ORDER BY b.name = 'main', b.updated_at DESC LIMIT 1) rb ON true
WHERE r.id = ANY (@repo_ids::uuid[])
  AND (sqlc.narg(before_at)::timestamptz IS NULL OR (a.at, a.id) < (sqlc.narg(before_at)::timestamptz, @before_id::uuid))
ORDER BY a.at DESC, a.id DESC
LIMIT @lim;
