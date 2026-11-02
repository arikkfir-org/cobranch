-- Model providers, and each agent's model and secrets: an organization adds a provider by the organization secret that
-- holds its API key, and an admin puts each agent on a model of one of them and names the secrets its tasks receive
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md, "Providers and models").
-- +goose Up

-- A provider an organization added: the provider's name, as the installation knows it, and the organization secret
-- that holds its API key. A provider whose secret is deleted keeps its row, without a secret: it shows as broken until
-- someone names another. Each change bumps its version, which is its ETag.
CREATE TABLE model_providers (
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider ~ '^[a-z][a-z0-9-]{0,39}$'),
    secret_id uuid REFERENCES secrets (id) ON DELETE SET NULL,
    created_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, provider)
);
CREATE INDEX model_providers_secret ON model_providers (secret_id) WHERE secret_id IS NOT NULL;
CREATE TRIGGER bump_version BEFORE UPDATE ON model_providers FOR EACH ROW EXECUTE FUNCTION bump_version();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON model_providers FOR EACH ROW EXECUTE FUNCTION audit();

-- An agent runs on a model of one of its organization's providers, or on none until an admin picks one; a provider an
-- agent runs on can't be removed. Its tasks receive its secrets, each an environment variable named by its key, from
-- the secret its value names.
ALTER TABLE agents
    ADD COLUMN provider text,
    ADD COLUMN model text CHECK (char_length(model) BETWEEN 1 AND 200),
    ADD COLUMN secrets jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(secrets) = 'object'),
    ADD CONSTRAINT agents_model CHECK ((provider IS NULL) = (model IS NULL)),
    ADD CONSTRAINT agents_provider FOREIGN KEY (org_id, provider) REFERENCES model_providers (org_id, provider);

-- +goose Down
ALTER TABLE agents DROP CONSTRAINT agents_provider, DROP CONSTRAINT agents_model, DROP COLUMN secrets, DROP COLUMN model,
    DROP COLUMN provider;
DROP TABLE model_providers;
