-- name: InsertUser :exec
INSERT INTO principals (id, kind, display_name) VALUES ($1, 'user', $2);

-- name: InsertVerifiedEmail :exec
INSERT INTO principal_emails (email, principal_id, verified) VALUES ($1, $2, true);

-- name: InsertSeedIdentity :exec
INSERT INTO identities (issuer, subject, principal_id, email, email_verified, name)
VALUES (@issuer, @subject, @principal_id, @email, true, @name);

-- name: InsertSeedGrant :exec
INSERT INTO grants (principal_id, role_id, scope, scope_id, source) VALUES (@principal_id, @role_id, @scope, @scope_id, 'grant');

-- name: InsertSeedOrganization :exec
INSERT INTO organizations (id, slug, name, membership_role_id) VALUES (@id, @slug, @slug_name, @membership_role_id);

-- name: InsertSeedSlug :exec
INSERT INTO organization_slugs (slug, org_id) VALUES ($1, $2);

-- name: InsertSeedMembership :exec
INSERT INTO memberships (org_id, user_id) VALUES ($1, $2);

-- name: InsertSeedInvitation :exec
INSERT INTO invitations (id, org_id, email, role_id, invited_by, expires_at)
VALUES (@id, @org_id, @email, @role_id, @invited_by, @expires_at);

-- name: InsertSeedRole :exec
INSERT INTO roles (id, scope, name, description, permissions, built_in, org_id)
VALUES (@id, 'organization', @name, @description, string_to_array(@permissions::text, ','), false, @org_id);

-- name: InsertSeedAPIKey :exec
INSERT INTO principals (id, kind, display_name, org_id, created_by) VALUES (@id, 'api_key', @name, @org_id, @created_by);

-- name: InsertSeedCredential :exec
INSERT INTO credentials (id, kind, token_hash, principal_id, hint, created_at, created_by, expires_at, revoked_at)
VALUES (@id, 'api_key', @token_hash, @principal_id, @hint, now(), @created_by, @expires_at, @revoked_at);

-- name: InsertSeedRepository :exec
INSERT INTO repositories (id, org_id, slug, name, description, created_by) VALUES (@id, @org_id, @slug, @name, @description, @created_by);

-- name: InsertSeedRepositorySlug :exec
INSERT INTO repository_slugs (org_id, slug, repo_id) VALUES ($1, $2, $3);

-- name: InsertSeedIssue :exec
INSERT INTO issues (id, org_id, number, title, description, state, priority, assignee_id, repo_id, created_by, closed_at)
VALUES (@id, @org_id, @number, @title, @description, @state, @priority, sqlc.narg(assignee_id), sqlc.narg(repo_id), @created_by,
        CASE WHEN @state::text IN ('done', 'canceled') THEN now() END);

-- name: InsertSeedIssueNumber :exec
INSERT INTO issue_numbers (org_id, last) VALUES ($1, $2);

-- name: InsertSeedEvent :exec
INSERT INTO events (actor_id, kind, subject_id, details, org_id) VALUES (@actor_id, @kind, @subject_id, @details, @org_id);
