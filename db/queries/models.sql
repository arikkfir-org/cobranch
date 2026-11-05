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
-- version when version is negative, and answers its version and what Cobranch made it for: none otherwise.
-- name: SetAgentModel :one
UPDATE agents SET provider = sqlc.narg(provider), model = sqlc.narg(model), updated_at = now()
WHERE principal_id = @id AND org_id = @org_id AND (@version::bigint < 0 OR version = @version::bigint)
RETURNING version, purpose;

-- SetAgentSecrets names the secrets an agent's tasks receive, when it is at version, or at any version when version
-- is negative: none otherwise.
-- name: SetAgentSecrets :one
UPDATE agents SET secrets = @secrets, updated_at = now()
WHERE principal_id = @id AND org_id = @org_id AND (@version::bigint < 0 OR version = @version::bigint)
RETURNING version;

-- Models lists an organization's models, by provider and name, with the names of their creators.
-- name: Models :many
SELECT sqlc.embed(m), c.display_name AS creator_name
FROM models m LEFT JOIN principals c ON c.id = m.created_by
WHERE m.org_id = @org_id ORDER BY m.provider, m.model;

-- Model finds one of an organization's models, with the name of its creator.
-- name: Model :one
SELECT sqlc.embed(m), c.display_name AS creator_name
FROM models m LEFT JOIN principals c ON c.id = m.created_by
WHERE m.org_id = @org_id AND m.provider = @provider AND m.model = @name;

-- ModelAgents lists the agents of an organization that run on its models, by name, with the model each runs on.
-- name: ModelAgents :many
SELECT principal_id, name, provider::text AS provider, model::text AS model FROM agents
WHERE org_id = @org_id AND provider IS NOT NULL ORDER BY lower(name), principal_id;

-- LockModel holds one of an organization's models until the transaction ends, and answers its version.
-- name: LockModel :one
SELECT version FROM models WHERE org_id = @org_id AND provider = @provider AND model = @model FOR UPDATE;

-- ShareModel holds one of an organization's models against removal until the transaction ends.
-- name: ShareModel :one
SELECT version FROM models WHERE org_id = @org_id AND provider = @provider AND model = @model FOR SHARE;

-- InsertModel adds a model to an organization, unless it has it.
-- name: InsertModel :execrows
INSERT INTO models (org_id, provider, model, input_price, output_price, cap, is_default, created_by)
VALUES (@org_id, @provider, @model, @input_price, @output_price, sqlc.narg(cap), @is_default, sqlc.narg(created_by))
ON CONFLICT (org_id, provider, model) DO NOTHING;

-- UpdateModel changes what one of an organization's models costs, its cap and whether it is the default.
-- name: UpdateModel :exec
UPDATE models SET input_price = @input_price, output_price = @output_price, cap = sqlc.narg(cap), is_default = @is_default,
    updated_at = now()
WHERE org_id = @org_id AND provider = @provider AND model = @model;

-- UnsetDefaultModel makes none of an organization's models but the one named its default.
-- name: UnsetDefaultModel :exec
UPDATE models SET is_default = false, updated_at = now()
WHERE org_id = @org_id AND is_default AND NOT (provider = @provider AND model = @model);

-- DeleteModel removes a model from an organization.
-- name: DeleteModel :exec
DELETE FROM models WHERE org_id = @org_id AND provider = @provider AND model = @model;

-- ModelAgentCount counts an organization's agents that run on one of its models.
-- name: ModelAgentCount :one
SELECT count(*) FROM agents WHERE org_id = @org_id AND provider = @provider AND model = @model;

-- StartOnDefaultModel puts a new agent of an organization on the organization's default model, when it has one.
-- name: StartOnDefaultModel :exec
UPDATE agents a SET provider = m.provider, model = m.model
FROM models m WHERE a.principal_id = @id AND m.org_id = a.org_id AND m.is_default;
