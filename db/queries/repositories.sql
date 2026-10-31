-- name: InsertRepository :exec
INSERT INTO repositories (id, org_id, slug, name, description, object_format, created_by, created_at)
VALUES (@id, @org_id, @slug, @name, @description, @object_format, @created_by, @created_at);

-- ClaimRepositorySlug gives the repository the slug in its organization, taking it from the repository that had it
-- before, whose old slug stops redirecting; repositories' own unique slug refuses one another repository has now.
-- name: ClaimRepositorySlug :exec
INSERT INTO repository_slugs (org_id, slug, repo_id) VALUES ($1, $2, $3)
ON CONFLICT (org_id, slug) DO UPDATE SET repo_id = EXCLUDED.repo_id, created_at = now()
WHERE repository_slugs.repo_id <> EXCLUDED.repo_id;

-- RepositoryBySlug finds one of the organization's repositories by any slug it has had there.
-- name: RepositoryBySlug :one
SELECT sqlc.embed(r) FROM repositories r JOIN repository_slugs s ON s.repo_id = r.id WHERE s.org_id = $1 AND s.slug = $2;

-- name: RepositoryByID :one
SELECT * FROM repositories WHERE id = $1;

-- name: UpdateRepository :execrows
UPDATE repositories SET slug = @slug, name = @name, description = @description
WHERE id = @id;

-- RepositoriesAmong lists those of ids that are the organization's repositories, by slug.
-- name: RepositoriesAmong :many
SELECT * FROM repositories WHERE org_id = @org_id AND id = ANY (@ids::uuid[]) ORDER BY slug;

-- RepositoryPermissions lists the permissions the principal holds at the repository: those of the roles its grants
-- there give, and those of its roles at the repository's organization, each kept to the repository's own, which
-- repo_permissions lists.
-- name: RepositoryPermissions :many
SELECT DISTINCT p.permission::text AS permission FROM (
    SELECT unnest(r.permissions) AS permission FROM grants g JOIN roles r ON r.id = g.role_id
    WHERE g.principal_id = @principal_id AND g.scope = 'repository' AND g.scope_id = @repo_id::uuid
    UNION ALL
    SELECT unnest(r.permissions) FROM grants g JOIN roles r ON r.id = g.role_id JOIN repositories rp ON rp.org_id = g.scope_id
    WHERE g.principal_id = @principal_id AND g.scope = 'organization' AND rp.id = @repo_id::uuid
) p WHERE p.permission = ANY (@repo_permissions::text[])
ORDER BY 1;

-- RepositoriesWith lists the IDs of the organization's repositories where the principal holds the permission,
-- through a grant on the repository or a role at the organization: every one of them when a role there holds it.
-- name: RepositoriesWith :many
SELECT rp.id FROM repositories rp WHERE rp.org_id = @org_id AND (
    EXISTS (SELECT 1 FROM grants g JOIN roles r ON r.id = g.role_id
            WHERE g.principal_id = @principal_id AND g.scope = 'organization' AND g.scope_id = rp.org_id
              AND @permission::text = ANY (r.permissions))
    OR EXISTS (SELECT 1 FROM grants g JOIN roles r ON r.id = g.role_id
               WHERE g.principal_id = @principal_id AND g.scope = 'repository' AND g.scope_id = rp.id
                 AND @permission::text = ANY (r.permissions)))
ORDER BY rp.slug;

-- SharedRepositories lists the IDs of the organization's repositories that someone besides their creator and the
-- organization's Owners reads, through a grant on the repository or a role at the organization: the Internal ones.
-- name: SharedRepositories :many
SELECT rp.id FROM repositories rp WHERE rp.org_id = @org_id AND EXISTS (
    SELECT 1 FROM grants g JOIN roles r ON r.id = g.role_id
    WHERE 'repo.read' = ANY (r.permissions) AND g.principal_id IS DISTINCT FROM rp.created_by
      AND ((g.scope = 'repository' AND g.scope_id = rp.id) OR (g.scope = 'organization' AND g.scope_id = rp.org_id))
      AND NOT EXISTS (SELECT 1 FROM grants o WHERE o.principal_id = g.principal_id AND o.role_id = @owner_role_id
                        AND o.scope = 'organization' AND o.scope_id = rp.org_id));

-- RepositoryGrants lists the roles every principal holds on the repository, with who they are, by display name.
-- name: RepositoryGrants :many
SELECT g.principal_id, p.kind, p.display_name, sqlc.embed(r) FROM grants g JOIN roles r ON r.id = g.role_id
JOIN principals p ON p.id = g.principal_id
WHERE g.scope = 'repository' AND g.scope_id = @repo_id::uuid ORDER BY p.display_name, g.principal_id, r.name;

-- RepositoryRoles lists the roles a repository grants: the built-in repository roles first, in the order migration 2
-- gives them, then the organization's own.
-- name: RepositoryRoles :many
SELECT * FROM roles WHERE (scope = 'repository' AND built_in) OR org_id = @org_id::uuid ORDER BY built_in DESC, id;

-- Grantee finds a principal that may hold grants in the organization: one of its members, or one of its API keys.
-- name: Grantee :one
SELECT p.id, p.kind, p.display_name FROM principals p
WHERE p.id = @principal_id AND (p.org_id = @org_id::uuid
    OR EXISTS (SELECT 1 FROM memberships m WHERE m.org_id = @org_id::uuid AND m.user_id = p.id));

-- DeleteRepositoryGrant takes one role from the principal on the repository.
-- name: DeleteRepositoryGrant :execrows
DELETE FROM grants WHERE principal_id = $1 AND role_id = $2 AND scope = 'repository' AND scope_id = @repo_id::uuid;

-- DeleteRepositoryGrantsAt takes every role the principal holds on the organization's repositories: they go with the
-- membership.
-- name: DeleteRepositoryGrantsAt :execrows
DELETE FROM grants g USING repositories r
WHERE g.principal_id = @principal_id AND g.scope = 'repository' AND g.scope_id = r.id AND r.org_id = @org_id::uuid;

-- RepositoryByPath finds a repository by its organization's slug and its own, each one it has or had, as Git's URLs
-- name them.
-- name: RepositoryByPath :one
SELECT sqlc.embed(r) FROM repositories r
JOIN repository_slugs rs ON rs.repo_id = r.id
JOIN organization_slugs os ON os.org_id = r.org_id
WHERE os.slug = @org_slug AND rs.slug = @repo_slug;
