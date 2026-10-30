-- name: Identity :one
SELECT * FROM identities WHERE issuer = $1 AND subject = $2;

-- name: InsertIdentity :exec
INSERT INTO identities (issuer, subject, principal_id, email, email_verified, name, created_at, last_sign_in_at)
VALUES (@issuer, @subject, @principal_id, @email, @email_verified, @name, @signed_in_at, @signed_in_at);

-- UpdateIdentity records the identity's latest claims and sign-in.
-- name: UpdateIdentity :execrows
UPDATE identities SET email = @email, email_verified = @email_verified, name = @name, last_sign_in_at = @signed_in_at
WHERE issuer = @issuer AND subject = @subject;

-- SeededUsers lists the users with an identity of the issuer, by display name.
-- name: SeededUsers :many
SELECT i.subject, p.display_name, i.email FROM principals p JOIN identities i ON i.principal_id = p.id
WHERE i.issuer = $1 ORDER BY p.display_name;
