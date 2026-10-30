-- name: InsertPrincipal :exec
INSERT INTO principals (id, kind, display_name, created_at) VALUES ($1, $2, $3, $4);

-- name: InsertEmail :exec
INSERT INTO principal_emails (email, principal_id, verified) VALUES ($1, $2, $3);

-- name: Emails :many
SELECT email, verified FROM principal_emails WHERE principal_id = $1 ORDER BY created_at;

-- EmailHolder finds the user who holds the email, verified, and the issuer of their latest identity, or an empty issuer without one.
-- name: EmailHolder :one
SELECT e.principal_id,
    coalesce((SELECT i.issuer FROM identities i WHERE i.principal_id = e.principal_id
        ORDER BY i.last_sign_in_at DESC LIMIT 1), '')::text AS issuer
FROM principal_emails e WHERE e.email = $1 AND e.verified;
