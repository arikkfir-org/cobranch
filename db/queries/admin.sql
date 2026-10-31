-- The Back-office's lists: every organization and every user, newest first by ID (a UUIDv7), a page after a cursor's
-- ID, matching a pattern (ILIKE, its wildcards escaped by the caller).

-- AdminOrganizations lists the organizations whose slug or name matches the pattern, with their member counts.
-- name: AdminOrganizations :many
SELECT sqlc.embed(o), (SELECT count(*) FROM memberships m WHERE m.org_id = o.id) AS members
FROM organizations o
WHERE (sqlc.narg(after)::uuid IS NULL OR o.id < sqlc.narg(after)::uuid)
    AND (o.slug ILIKE @pattern OR o.name ILIKE @pattern)
ORDER BY o.id DESC
LIMIT @max_rows;

-- AdminUsers lists the users whose display name or an email matches the pattern, with their emails (and whether each is
-- verified, in the same order), the issuers they sign in with, their latest sign-in (their sign-up, itself a sign-in,
-- without an identity), the roles they hold at the installation, and how many organizations they belong to.
-- name: AdminUsers :many
SELECT p.id, p.display_name, p.suspended, p.created_at,
    coalesce((SELECT array_agg(e.email ORDER BY e.email) FROM principal_emails e WHERE e.principal_id = p.id), '{}')::text[] AS emails,
    coalesce((SELECT array_agg(e.verified ORDER BY e.email) FROM principal_emails e WHERE e.principal_id = p.id), '{}')::boolean[] AS verified,
    coalesce((SELECT array_agg(DISTINCT i.issuer ORDER BY i.issuer) FROM identities i WHERE i.principal_id = p.id), '{}')::text[] AS issuers,
    coalesce((SELECT max(i.last_sign_in_at) FROM identities i WHERE i.principal_id = p.id), p.created_at)::timestamptz AS last_sign_in_at,
    coalesce((SELECT array_agg(r.name ORDER BY r.name) FROM grants g JOIN roles r ON r.id = g.role_id
        WHERE g.principal_id = p.id AND g.scope = 'installation'), '{}')::text[] AS roles,
    (SELECT count(*) FROM memberships m WHERE m.user_id = p.id) AS organizations
FROM principals p
WHERE p.kind = 'user' AND (sqlc.narg(after)::uuid IS NULL OR p.id < sqlc.narg(after)::uuid)
    AND (p.display_name ILIKE @pattern OR EXISTS (SELECT 1 FROM principal_emails e WHERE e.principal_id = p.id AND e.email ILIKE @pattern))
ORDER BY p.id DESC
LIMIT @max_rows;
