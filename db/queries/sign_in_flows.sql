-- name: CreateFlow :exec
INSERT INTO sign_in_flows (state_hash, browser_hash, provider_id, nonce, pkce_verifier, return_path, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- TakeFlow deletes the flow and returns it, so each is finished once.
-- name: TakeFlow :one
DELETE FROM sign_in_flows WHERE state_hash = $1 RETURNING *;

-- name: DeleteExpiredFlows :execrows
DELETE FROM sign_in_flows WHERE expires_at < @now;
