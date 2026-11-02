-- ModelProviders lists the providers an organization added, by name, with the names of their secrets.
-- name: ModelProviders :many
SELECT sqlc.embed(p), s.name AS secret_name, c.display_name AS creator_name
FROM model_providers p LEFT JOIN secrets s ON s.id = p.secret_id LEFT JOIN principals c ON c.id = p.created_by
WHERE p.org_id = @org_id ORDER BY p.provider;

-- ModelProvider finds a provider an organization added, with the name of its secret.
-- name: ModelProvider :one
SELECT sqlc.embed(p), s.name AS secret_name, c.display_name AS creator_name
FROM model_providers p LEFT JOIN secrets s ON s.id = p.secret_id LEFT JOIN principals c ON c.id = p.created_by
WHERE p.org_id = @org_id AND p.provider = @provider;

-- LockModelProvider holds a provider an organization added until the transaction ends, and answers its version.
-- name: LockModelProvider :one
SELECT version FROM model_providers WHERE org_id = @org_id AND provider = @provider FOR UPDATE;

-- ShareModelProvider holds a provider an organization added against removal until the transaction ends.
-- name: ShareModelProvider :one
SELECT version FROM model_providers WHERE org_id = @org_id AND provider = @provider FOR SHARE;

-- InsertModelProvider adds a provider to an organization, unless it has it.
-- name: InsertModelProvider :execrows
INSERT INTO model_providers (org_id, provider, secret_id, created_by) VALUES (@org_id, @provider, @secret_id, sqlc.narg(created_by))
ON CONFLICT (org_id, provider) DO NOTHING;

-- UpdateModelProvider names another secret for a provider an organization added.
-- name: UpdateModelProvider :exec
UPDATE model_providers SET secret_id = @secret_id, updated_at = now() WHERE org_id = @org_id AND provider = @provider;

-- DeleteModelProvider removes a provider from an organization.
-- name: DeleteModelProvider :exec
DELETE FROM model_providers WHERE org_id = @org_id AND provider = @provider;

-- ProviderAgents counts an organization's agents that run on a provider.
-- name: ProviderAgents :one
SELECT count(*) FROM agents WHERE org_id = @org_id AND provider = @provider;

-- SecretRepository finds one of an organization's secrets and answers the repository whose settings hold it: null for
-- the organization's own.
-- name: SecretRepository :one
SELECT repo_id FROM secrets WHERE id = @id AND org_id = @org_id;

-- SetAgentModel puts an agent on a model of a provider, or on none with nulls, when it is at version, or at any
-- version when version is negative: none otherwise.
-- name: SetAgentModel :one
UPDATE agents SET provider = sqlc.narg(provider), model = sqlc.narg(model), updated_at = now()
WHERE principal_id = @id AND org_id = @org_id AND (@version::bigint < 0 OR version = @version::bigint)
RETURNING version;

-- SetAgentSecrets names the secrets an agent's tasks receive, when it is at version, or at any version when version
-- is negative: none otherwise.
-- name: SetAgentSecrets :one
UPDATE agents SET secrets = @secrets, updated_at = now()
WHERE principal_id = @id AND org_id = @org_id AND (@version::bigint < 0 OR version = @version::bigint)
RETURNING version;
