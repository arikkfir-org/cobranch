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
