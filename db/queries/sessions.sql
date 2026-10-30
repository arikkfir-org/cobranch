-- name: InsertSession :exec
INSERT INTO sessions (id, token_hash, principal_id, issuer, subject, created_at, last_seen_at, idle_expires_at,
    expires_at, user_agent, ip)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- SessionByToken finds a session by its token's hash, with its principal.
-- name: SessionByToken :one
SELECT sqlc.embed(s), sqlc.embed(p) FROM sessions s JOIN principals p ON p.id = s.principal_id WHERE s.token_hash = $1;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = @seen_at, idle_expires_at = @idle_expires_at WHERE id = @session_id;

-- Sessions lists a principal's sessions that are not revoked, newest first.
-- name: Sessions :many
SELECT * FROM sessions WHERE principal_id = $1 AND revoked_at IS NULL ORDER BY created_at DESC;

-- name: RevokeSession :execrows
UPDATE sessions SET revoked_at = @revoked_at::timestamptz WHERE id = @session_id AND principal_id = @principal_id AND revoked_at IS NULL;

-- DeleteExpiredSessions deletes the sessions that aged out, idled out or were revoked before now.
-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE LEAST(idle_expires_at, expires_at) < @now::timestamptz OR revoked_at < @now::timestamptz;
