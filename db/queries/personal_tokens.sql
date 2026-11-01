-- name: InsertPersonalToken :exec
INSERT INTO credentials (id, kind, token_hash, principal_id, hint, created_at, created_by, expires_at, name, permissions,
    issuer, subject)
VALUES (@id, 'personal_token', @token_hash, @principal_id, @hint, @created_at, @principal_id, @expires_at::timestamptz,
    @name, @permissions::text[], @issuer, @subject);

-- PersonalTokens lists the user's personal tokens that are not revoked, expired ones included, newest first.
-- name: PersonalTokens :many
SELECT * FROM credentials WHERE principal_id = @principal_id AND kind = 'personal_token' AND revoked_at IS NULL
ORDER BY created_at DESC, id;

-- name: RevokePersonalToken :execrows
UPDATE credentials SET revoked_at = @revoked_at::timestamptz
WHERE id = @id AND principal_id = @principal_id AND kind = 'personal_token' AND revoked_at IS NULL;
