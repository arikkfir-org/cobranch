-- SecretPermissions lists the permissions the principal holds on a secret: those of the roles its grants give on the
-- secret, on its repository and at its organization, each kept to the secret permissions, which permissions
-- lists.
-- name: SecretPermissions :many
SELECT DISTINCT p.permission::text AS permission FROM (
    SELECT unnest(r.permissions) AS permission FROM grants g JOIN roles r ON r.id = g.role_id
    WHERE g.principal_id = @principal_id AND g.scope = 'secret' AND g.scope_id = @secret_id::uuid
    UNION ALL
    SELECT unnest(r.permissions) FROM grants g JOIN roles r ON r.id = g.role_id JOIN secrets s ON s.repo_id = g.scope_id
    WHERE g.principal_id = @principal_id AND g.scope = 'repository' AND s.id = @secret_id::uuid
    UNION ALL
    SELECT unnest(r.permissions) FROM grants g JOIN roles r ON r.id = g.role_id JOIN secrets s ON s.org_id = g.scope_id
    WHERE g.principal_id = @principal_id AND g.scope = 'organization' AND s.id = @secret_id::uuid
) p WHERE p.permission = ANY (@permissions::text[])
ORDER BY 1;

-- name: Secrets :many
SELECT id, org_id, repo_id, name, version, created_by, created_at, updated_by, updated_at FROM secrets
WHERE org_id = @org_id AND repo_id IS NOT DISTINCT FROM sqlc.narg(repo_id)::uuid ORDER BY name, id;

-- name: Secret :one
SELECT id, org_id, repo_id, name, version, created_by, created_at, updated_by, updated_at FROM secrets WHERE id = $1;

-- InsertSecret writes a secret where it lives, a repository's only if the repository is the organization's: no row
-- otherwise.
-- name: InsertSecret :one
INSERT INTO secrets (id, org_id, repo_id, name, value, created_by, updated_by)
SELECT @id, @org_id, sqlc.narg(repo_id)::uuid, @name, @value, @actor::uuid, @actor::uuid
WHERE sqlc.narg(repo_id)::uuid IS NULL OR EXISTS (SELECT 1 FROM repositories WHERE id = sqlc.narg(repo_id)::uuid AND org_id = @org_id)
RETURNING id, org_id, repo_id, name, version, created_by, created_at, updated_by, updated_at;

-- SecretForUpdate locks a secret to the end of the transaction.
-- name: SecretForUpdate :one
SELECT id, org_id, repo_id, name, version, created_by, created_at, updated_by, updated_at FROM secrets WHERE id = $1 FOR UPDATE;

-- ReplaceSecret renames a secret, and replaces its value unless value is NULL.
-- name: ReplaceSecret :one
UPDATE secrets SET name = @name, value = coalesce(sqlc.narg(value)::bytea, value), updated_by = @actor::uuid, updated_at = now()
WHERE id = @id
RETURNING id, org_id, repo_id, name, version, created_by, created_at, updated_by, updated_at;

-- DeleteSecret deletes a secret; trigger delete_grants takes the grants on it.
-- name: DeleteSecret :exec
DELETE FROM secrets WHERE id = $1;

-- name: SealedSecrets :many
SELECT id, value FROM secrets WHERE id = ANY (@ids::uuid[]);

-- SecretGrantees lists the principals holding a grant on a secret, by display name.
-- name: SecretGrantees :many
SELECT DISTINCT p.id, p.kind, p.display_name FROM grants g JOIN principals p ON p.id = g.principal_id
WHERE g.scope = 'secret' AND g.scope_id = @secret_id::uuid ORDER BY p.display_name, p.id;

-- name: DeleteSecretGrant :execrows
DELETE FROM grants WHERE principal_id = @principal_id AND role_id = @role_id AND scope = 'secret' AND scope_id = @secret_id::uuid;

-- RepositoryJobs lists a repository's jobs, the principals its secrets are granted to, by name.
-- name: RepositoryJobs :many
SELECT j.principal_id AS id, j.name FROM jobs j WHERE j.org_id = @org_id AND j.repo_id = @repo_id ORDER BY j.name;
