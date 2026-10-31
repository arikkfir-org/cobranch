-- The identity providers the Back-office adds: the Auth server reads them at each sign-in, the Admin server changes
-- them.

-- IdentityProviders lists the providers by ID.
-- name: IdentityProviders :many
SELECT * FROM identity_providers ORDER BY id;

-- IdentityProvider reads one provider.
-- name: IdentityProvider :one
SELECT * FROM identity_providers WHERE id = @id;

-- CreateIdentityProvider adds a provider.
-- name: CreateIdentityProvider :one
INSERT INTO identity_providers (id, name, issuer, preset, client_id, client_secret, created_at, updated_at, updated_by)
VALUES (@id, @name, sqlc.narg(issuer), sqlc.narg(preset), @client_id, @client_secret, @updated_at, @updated_at, @updated_by)
RETURNING *;

-- ChangeIdentityProvider writes a provider, by who, unless its version is no longer the one the change read: then it
-- changes no row.
-- name: ChangeIdentityProvider :one
UPDATE identity_providers
SET name = @name, issuer = sqlc.narg(issuer), preset = sqlc.narg(preset), client_id = @client_id,
    client_secret = @client_secret, updated_at = @updated_at, updated_by = @updated_by
WHERE id = @id AND version = @version
RETURNING *;

-- DeleteIdentityProvider removes a provider, unless its version is no longer the one the removal read: then it removes
-- no row.
-- name: DeleteIdentityProvider :execrows
DELETE FROM identity_providers WHERE id = @id AND version = @version;
