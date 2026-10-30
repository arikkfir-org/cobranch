-- name: InsertUser :exec
INSERT INTO principals (id, kind, display_name) VALUES ($1, 'user', $2);

-- name: InsertVerifiedEmail :exec
INSERT INTO principal_emails (email, principal_id, verified) VALUES ($1, $2, true);

-- name: InsertSeedIdentity :exec
INSERT INTO identities (issuer, subject, principal_id, email, email_verified, name)
VALUES (@issuer, @subject, @principal_id, @email, true, @name);
