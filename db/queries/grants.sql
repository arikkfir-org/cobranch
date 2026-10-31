-- InsertInstallationGrant gives the principal the role at the installation, once: a grant it has is kept as it is.
-- name: InsertInstallationGrant :execrows
INSERT INTO grants (principal_id, role_id, scope, source) VALUES ($1, $2, 'installation', $3) ON CONFLICT DO NOTHING;

-- name: DeleteGrant :execrows
DELETE FROM grants WHERE principal_id = $1 AND role_id = $2 AND source = $3;

-- name: InsertEvent :exec
INSERT INTO events (actor_id, kind, subject_id, details, org_id) VALUES ($1, $2, $3, $4, $5);
