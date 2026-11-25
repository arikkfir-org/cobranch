-- InsertOrganization makes an organization; created_by is the user it counts against, NULL for one an administrator
-- made for its first Owner.
-- name: InsertOrganization :exec
INSERT INTO organizations (id, slug, name, membership_role_id, accepts_join_requests, providers, issue_key, created_at, created_by)
VALUES (@id, @slug, @name, @membership_role_id, @accepts_join_requests, @providers, @issue_key, @created_at, @created_by);

-- CreatedOrganizations counts the organizations the user created for themselves that aren't being deleted.
-- name: CreatedOrganizations :one
SELECT count(*) FROM organizations WHERE created_by = @user_id::uuid AND deleting_at IS NULL;

-- InsertSlug claims a slug for an organization; the primary key refuses one another organization has or had.
-- name: InsertSlug :exec
INSERT INTO organization_slugs (slug, org_id) VALUES ($1, $2);

-- OrganizationBySlug finds a live organization by any slug it has had.
-- name: OrganizationBySlug :one
SELECT sqlc.embed(o) FROM organizations o JOIN organization_slugs s ON s.org_id = o.id WHERE s.slug = $1 AND o.deleting_at IS NULL;

-- OrganizationByID finds a live organization: one being deleted is gone for everyone.
-- name: OrganizationByID :one
SELECT * FROM organizations WHERE id = $1 AND deleting_at IS NULL;

-- UpdateOrganization writes a live organization's settings and answers its version since.
-- name: UpdateOrganization :one
UPDATE organizations SET slug = @slug, name = @name, membership_role_id = @membership_role_id,
    accepts_join_requests = @accepts_join_requests, providers = @providers, issue_key = @issue_key
WHERE id = @id AND deleting_at IS NULL
RETURNING version;

-- OrganizationsOf lists the live organizations a user belongs to, by name.
-- name: OrganizationsOf :many
SELECT sqlc.embed(o) FROM organizations o JOIN memberships m ON m.org_id = o.id
WHERE m.user_id = $1 AND o.deleting_at IS NULL ORDER BY o.name, o.slug;

-- name: InsertMembership :exec
INSERT INTO memberships (org_id, user_id, joined_at) VALUES ($1, $2, $3);

-- name: DeleteMembership :execrows
DELETE FROM memberships WHERE org_id = $1 AND user_id = $2;

-- name: Membership :one
SELECT * FROM memberships WHERE org_id = $1 AND user_id = $2;

-- Members lists an organization's members, by display name, each with an email of theirs: a verified one, the oldest
-- first, or else their oldest, or none.
-- name: Members :many
SELECT m.user_id, m.joined_at, p.display_name,
       coalesce((SELECT e.email FROM principal_emails e WHERE e.principal_id = m.user_id
                 ORDER BY e.verified DESC, e.created_at, e.email LIMIT 1), '')::text AS email
FROM memberships m JOIN principals p ON p.id = m.user_id
WHERE m.org_id = $1 ORDER BY p.display_name, m.user_id;

-- MemberByEmail finds the member who holds the email, verified.
-- name: MemberByEmail :one
SELECT m.user_id FROM memberships m JOIN principal_emails e ON e.principal_id = m.user_id
WHERE m.org_id = $1 AND e.email = $2 AND e.verified;

-- OrganizationGrants lists the roles every principal holds at the organization.
-- name: OrganizationGrants :many
SELECT g.principal_id, sqlc.embed(r) FROM grants g JOIN roles r ON r.id = g.role_id
WHERE g.scope = 'organization' AND g.scope_id = @org_id::uuid ORDER BY g.principal_id, r.name;

-- InsertGrant gives the principal the role at the scope, once: a grant it has is kept as it is.
-- name: InsertGrant :execrows
INSERT INTO grants (principal_id, role_id, scope, scope_id, source) VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING;

-- DeleteScopeGrants takes every role the principal holds at the scope.
-- name: DeleteScopeGrants :execrows
DELETE FROM grants WHERE principal_id = @principal_id AND scope = @scope AND scope_id = @scope_id::uuid;

-- CountRoleHolders counts the principals holding the role at the organization.
-- name: CountRoleHolders :one
SELECT count(*) FROM grants WHERE role_id = @role_id AND scope = 'organization' AND scope_id = @org_id::uuid;

-- Permissions lists the permissions the principal's roles hold at the scope: the installation when scope_id is NULL.
-- name: Permissions :many
SELECT DISTINCT unnest(r.permissions)::text AS permission FROM grants g JOIN roles r ON r.id = g.role_id
WHERE g.principal_id = @principal_id AND g.scope = @scope AND g.scope_id IS NOT DISTINCT FROM sqlc.narg(scope_id)::uuid
ORDER BY 1;

-- name: Role :one
SELECT * FROM roles WHERE id = $1;

-- ClaimSlug gives the organization the slug, or changes nothing when another organization has or had it: zero rows.
-- name: ClaimSlug :execrows
INSERT INTO organization_slugs (slug, org_id) VALUES ($1, $2)
ON CONFLICT (slug) DO UPDATE SET org_id = EXCLUDED.org_id WHERE organization_slugs.org_id = EXCLUDED.org_id;

-- LockOrganization holds a live organization's row until the transaction ends, so changes to who may act there
-- happen one at a time, and none once it is being deleted.
-- name: LockOrganization :one
SELECT id FROM organizations WHERE id = $1 AND deleting_at IS NULL FOR UPDATE;

-- name: HoldsRole :one
SELECT EXISTS (SELECT 1 FROM grants WHERE principal_id = $1 AND role_id = $2 AND scope = 'organization' AND scope_id = @org_id::uuid);

-- LockOrganizationVersion holds a live organization's row until the transaction ends, and answers its version.
-- name: LockOrganizationVersion :one
SELECT version FROM organizations WHERE id = $1 AND deleting_at IS NULL FOR UPDATE;

-- HoldRepositoryOrganization holds the organization of a repository against its deletion until the transaction ends,
-- since a deletion takes its row FOR UPDATE first, and answers whether it is live: false once it is being deleted. A
-- deletion that waited for the hold then cancels what was queued under it.
-- name: HoldRepositoryOrganization :one
SELECT (o.deleting_at IS NULL)::boolean AS live
FROM repositories r JOIN organizations o ON o.id = r.org_id
WHERE r.id = $1
FOR KEY SHARE OF o;

-- HoldOrganization holds a live organization against its deletion until the transaction ends, as
-- HoldRepositoryOrganization does a repository's, and answers whether it is live.
-- name: HoldOrganization :one
SELECT (deleting_at IS NULL)::boolean AS live FROM organizations WHERE id = $1 FOR KEY SHARE;

-- MarkOrganizationDeleting makes the organization Deleting, for whoever deletes it.
-- name: MarkOrganizationDeleting :exec
UPDATE organizations SET deleting_at = @deleting_at::timestamptz, deleted_by = @deleted_by::uuid WHERE id = @id;

-- DeleteOrganizationAccess frees the organization's slugs and deletes its memberships, invitations and join requests.
-- name: DeleteOrganizationAccess :exec
WITH slugs AS (
    DELETE FROM organization_slugs s WHERE s.org_id = @organization::uuid
), members AS (
    DELETE FROM memberships m WHERE m.org_id = @organization::uuid
), invited AS (
    DELETE FROM invitations i WHERE i.org_id = @organization::uuid
)
DELETE FROM join_requests j WHERE j.org_id = @organization::uuid;

-- DeleteOrganizationGrants deletes every grant there of whoever isn't one of the organization's own principals: at the
-- organization, at its repositories, secrets and artifacts, and of its roles. Its own principals keep theirs until the
-- purge deletes them: its jobs, agents and API keys act no more, and its targets tear its environments down.
-- name: DeleteOrganizationGrants :exec
DELETE FROM grants g USING principals p
WHERE p.id = g.principal_id AND p.org_id IS DISTINCT FROM @organization::uuid
  AND ((g.scope = 'organization' AND g.scope_id = @organization::uuid)
    OR (g.scope = 'repository' AND g.scope_id IN (SELECT r.id FROM repositories r WHERE r.org_id = @organization::uuid))
    OR (g.scope = 'secret' AND g.scope_id IN (SELECT s.id FROM secrets s WHERE s.org_id = @organization::uuid))
    OR (g.scope = 'artifact' AND g.scope_id IN (SELECT a.id FROM artifacts a WHERE a.org_id = @organization::uuid))
    OR g.role_id IN (SELECT r.id FROM roles r WHERE r.org_id = @organization::uuid));

-- DeleteOrganizationCredentials deletes the secrets of the organization's API keys.
-- name: DeleteOrganizationCredentials :exec
DELETE FROM credentials c USING principals p WHERE p.id = c.principal_id AND p.org_id = @organization::uuid;

-- CancelOrganizationWork cancels, with the reason, the organization's runs and tasks that haven't ended and its
-- deliveries that haven't started applying.
-- name: CancelOrganizationWork :exec
WITH runs AS (
    UPDATE job_runs r SET state = 'cancelled', reason = @reason::text, ended_at = now(), lease_until = NULL
    WHERE r.org_id = @organization::uuid AND r.state IN ('waiting', 'queued', 'running')
), tasks AS (
    UPDATE agent_tasks t SET state = 'cancelled', cancelled_by = @cancelled_by::uuid, reason = @reason::text, ended_at = now()
    WHERE t.org_id = @organization::uuid AND t.state IN ('queued', 'running', 'waiting')
)
UPDATE deliveries d SET state = 'cancelled', reason = @reason::text, ended_at = now(), lease_until = NULL
WHERE d.org_id = @organization::uuid AND d.state IN ('waiting', 'queued', 'rendering');

-- OrganizationsToPurge lists, the earliest deleted first, the organizations being deleted whose rows may go: nothing of
-- theirs delivers and each of their live environments had its teardown since, or they were deleted before @before,
-- whatever their teardowns do. Each comes with how many of its environments live still, which a teardown that failed
-- leaves.
-- name: OrganizationsToPurge :many
SELECT o.id, o.deleting_at::timestamptz AS deleting_at,
    (SELECT count(*) FROM environments e WHERE e.org_id = o.id AND e.deleted_at IS NULL) AS live_environments
FROM organizations o
WHERE o.deleting_at IS NOT NULL
  AND (o.deleting_at < @before::timestamptz OR (
    NOT EXISTS (SELECT 1 FROM deliveries d WHERE d.org_id = o.id
        AND d.state IN ('waiting', 'queued', 'rendering', 'applying', 'deleting'))
    AND NOT EXISTS (SELECT 1 FROM environments e WHERE e.org_id = o.id AND e.deleted_at IS NULL
        AND NOT EXISTS (SELECT 1 FROM deliveries t WHERE t.environment_id = e.id AND t.kind = 'teardown'
            AND t.queued_at >= o.deleting_at))))
ORDER BY o.deleting_at, o.id;

-- PurgeOrganization deletes an organization being deleted: its ON DELETE CASCADEs take every row that carries its
-- org_id.
-- name: PurgeOrganization :execrows
DELETE FROM organizations WHERE id = @id AND deleting_at IS NOT NULL;

-- DeleteOrganizationEvents deletes the events of an organization, which name it without a key.
-- name: DeleteOrganizationEvents :exec
DELETE FROM events WHERE org_id = @org_id::uuid;
