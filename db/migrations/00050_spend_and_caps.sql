-- Spend and caps: the models an organization's agents run on, each with what the organization pays for it and the most a
-- task on it may spend; an agent's own lower cap; and what each task spent, call by call, which the model proxy of the
-- task's pod reports (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md, "Spend and
-- caps"). Amounts are millionths of a US dollar: 1000000 is $1.
-- +goose Up

-- A model of a provider an organization added, which its agents may run on: what the organization pays per million
-- tokens the model reads and writes, from which Cobranch prices each call of it, and the most one task on it may spend,
-- or none. One of the organization's models is the default a new agent starts on. A model an agent runs on can't be
-- removed. Each change bumps its version, which is its ETag.
CREATE TABLE models (
    org_id uuid NOT NULL,
    provider text NOT NULL,
    model text NOT NULL CHECK (char_length(model) BETWEEN 1 AND 200),
    input_price bigint NOT NULL DEFAULT 0 CHECK (input_price BETWEEN 0 AND 10000000000),
    output_price bigint NOT NULL DEFAULT 0 CHECK (output_price BETWEEN 0 AND 10000000000),
    cap bigint CHECK (cap BETWEEN 1 AND 1000000000000),
    is_default boolean NOT NULL DEFAULT false,
    created_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, provider, model),
    FOREIGN KEY (org_id, provider) REFERENCES model_providers (org_id, provider) ON DELETE CASCADE
);
CREATE UNIQUE INDEX models_default ON models (org_id) WHERE is_default;
CREATE INDEX models_created_by ON models (created_by) WHERE created_by IS NOT NULL;
CREATE TRIGGER bump_version BEFORE UPDATE ON models FOR EACH ROW EXECUTE FUNCTION bump_version();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON models FOR EACH ROW EXECUTE FUNCTION audit();

-- Every model an agent runs on already becomes one of its organization's models, without prices or a cap, as it ran
-- until now.
INSERT INTO models (org_id, provider, model)
SELECT DISTINCT org_id, provider, model FROM agents WHERE provider IS NOT NULL;

-- An agent runs only on one of its organization's models, and may spend less on a task than its model allows: its own
-- cap, or none.
ALTER TABLE agents
    ADD COLUMN cap bigint CHECK (cap BETWEEN 1 AND 1000000000000),
    ADD CONSTRAINT agents_catalog_model FOREIGN KEY (org_id, provider, model) REFERENCES models (org_id, provider, model);

-- A call of a task's model, as the model proxy of the task's pod reported it, by an ID of its own so that a report it
-- repeats counts once: the model, the tokens it read and wrote, and what it cost at the organization's prices then.
-- Cobranch alone writes them and nobody changes one, so they aren't audited.
CREATE TABLE task_spend (
    task_id uuid NOT NULL REFERENCES agent_tasks (id) ON DELETE CASCADE,
    id uuid NOT NULL,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    agent_id uuid NOT NULL REFERENCES agents (principal_id) ON DELETE CASCADE,
    provider text NOT NULL,
    model text NOT NULL,
    input_tokens bigint NOT NULL CHECK (input_tokens >= 0),
    output_tokens bigint NOT NULL CHECK (output_tokens >= 0),
    cost bigint NOT NULL CHECK (cost >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, id)
);
CREATE INDEX task_spend_org ON task_spend (org_id, created_at);
CREATE INDEX task_spend_agent ON task_spend (agent_id, created_at);

-- +goose Down
DROP TABLE task_spend;
ALTER TABLE agents DROP CONSTRAINT agents_catalog_model, DROP COLUMN cap;
DROP TABLE models;
