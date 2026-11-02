-- The installation's settings, one row: sign-up reads them, and the Back-office changes them.

-- InstallationSettings reads the settings.
-- name: InstallationSettings :one
SELECT * FROM installation_settings;

-- ChangeInstallationSettings writes every setting, by who, unless the row's version is no longer the one the change
-- read: then it changes no row.
-- name: ChangeInstallationSettings :one
UPDATE installation_settings
SET sign_up = sqlc.narg(sign_up), sign_up_domains = @sign_up_domains, self_serve = sqlc.narg(self_serve),
    organizations_per_user = sqlc.narg(organizations_per_user), repositories_per_organization = sqlc.narg(repositories_per_organization),
    git_data_per_organization = sqlc.narg(git_data_per_organization), updated_at = @updated_at, updated_by = @updated_by
WHERE version = @version
RETURNING *;
