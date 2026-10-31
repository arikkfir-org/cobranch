-- name: InsertAPIKey :exec
INSERT INTO principals (id, kind, display_name, created_at, org_id, created_by)
VALUES (@id, 'api_key', @name, @created_at, @org_id, @created_by);

-- APIKeys lists the organization's API keys, by name.
-- name: APIKeys :many
SELECT * FROM principals WHERE kind = 'api_key' AND org_id = @org_id::uuid ORDER BY display_name, id;

-- APIKey finds one of the organization's API keys.
-- name: APIKey :one
SELECT * FROM principals WHERE id = @id AND kind = 'api_key' AND org_id = @org_id::uuid;

-- name: DeleteAPIKey :execrows
DELETE FROM principals WHERE id = @id AND kind = 'api_key' AND org_id = @org_id::uuid;

-- name: InsertCredential :exec
INSERT INTO credentials (id, kind, token_hash, principal_id, hint, created_at, created_by, expires_at)
VALUES (@id, 'api_key', @token_hash, @principal_id, @hint, @created_at, @created_by, @expires_at);

-- OrganizationCredentials lists the credentials of the organization's API keys, oldest first.
-- name: OrganizationCredentials :many
SELECT c.* FROM credentials c JOIN principals p ON p.id = c.principal_id
WHERE p.org_id = @org_id::uuid ORDER BY c.created_at, c.id;

-- CountLiveCredentials counts the principal's credentials that are neither revoked nor expired at now.
-- name: CountLiveCredentials :one
SELECT count(*) FROM credentials WHERE principal_id = @principal_id AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > @now::timestamptz);

-- name: RevokeCredential :execrows
UPDATE credentials SET revoked_at = @revoked_at::timestamptz
WHERE id = @id AND principal_id = @principal_id AND revoked_at IS NULL;

-- CredentialByToken finds a credential by its token's hash, with its principal.
-- name: CredentialByToken :one
SELECT sqlc.embed(c), sqlc.embed(p) FROM credentials c JOIN principals p ON p.id = c.principal_id WHERE c.token_hash = $1;

-- name: TouchCredential :exec
UPDATE credentials SET last_used_at = @used_at::timestamptz WHERE id = @id;
