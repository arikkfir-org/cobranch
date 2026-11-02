-- Quotas: the limits the installation's settings and an organization's own set, the locks under which a transaction
-- counts what a limit caps, and the usage it counts.

-- LockLimit holds a lock of the subject's, a user or an organization, for the limit named, until the transaction
-- ends: transactions that count what the limit caps there, and may add to it, run one at a time.
-- name: LockLimit :exec
SELECT pg_advisory_xact_lock(hashtext('cobranch.limit.' || sqlc.arg(limit_name)::text), hashtext(sqlc.arg(subject)::uuid::text));

-- InstallationLimits reads the limits the installation's settings set, NULL where they set none.
-- name: InstallationLimits :one
SELECT organizations_per_user, repositories_per_organization, git_data_per_organization FROM installation_settings;

-- OrganizationLimits reads the organization's own limits, NULL where it takes the installation's; no row for an
-- organization without any.
-- name: OrganizationLimits :one
SELECT repositories, git_data FROM organization_limits WHERE org_id = $1;

-- CountRepositories counts the organization's repositories.
-- name: CountRepositories :one
SELECT count(*) FROM repositories WHERE org_id = $1;

-- GitData sums the bytes of the organization's repositories' active packs.
-- name: GitData :one
SELECT coalesce(sum(size), 0)::bigint FROM packs WHERE org_id = $1 AND state = 'active';

-- PackActive reports whether the repository lists the pack, active: a push that brings it again adds no Git data.
-- name: PackActive :one
SELECT EXISTS (SELECT FROM packs WHERE repo_id = $1 AND name = $2 AND state = 'active');

-- OwnLimits reads the organization's own limits, with their version; no row for an organization without any yet.
-- name: OwnLimits :one
SELECT * FROM organization_limits WHERE org_id = $1;

-- InsertOwnLimits writes an organization's first own limits, by who; no row when another change wrote them first.
-- name: InsertOwnLimits :one
INSERT INTO organization_limits (org_id, repositories, git_data, updated_at, updated_by)
VALUES (@org_id, sqlc.narg(repositories), sqlc.narg(git_data), @updated_at, @updated_by)
ON CONFLICT (org_id) DO NOTHING
RETURNING *;

-- ChangeOwnLimits writes an organization's own limits, by who, unless their version is no longer the one the change
-- read: then it changes no row.
-- name: ChangeOwnLimits :one
UPDATE organization_limits
SET repositories = sqlc.narg(repositories), git_data = sqlc.narg(git_data), updated_at = @updated_at, updated_by = @updated_by
WHERE org_id = @org_id AND version = @version
RETURNING *;
