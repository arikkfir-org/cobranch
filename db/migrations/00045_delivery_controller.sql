-- The controller's deliveries into this installation's cluster: the token each render pod checks out and uploads with,
-- the render it uploads, and what each environment holds
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md, "Roots and rendering" and
-- "Applying"). The controller makes and deletes the tokens and records the inventories; the REST server keeps the
-- renders.
-- +goose Up

-- A delivery's token, by its hash: a credential of kind delivery_token, which acts as the delivery's target while the
-- delivery renders, reads its organization and repository, to check its commit out, and uploads its render, until it
-- expires with the render's timeout. Deliveries are history the controller writes, and their tokens with them, so
-- neither is audited.
CREATE TABLE delivery_tokens (
    delivery_id uuid PRIMARY KEY REFERENCES deliveries (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    hint text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    last_used_at timestamptz
);
CREATE INDEX delivery_tokens_expires ON delivery_tokens (expires_at);

-- A delivery's render, as its render pod uploaded it, once: one YAML stream of at most 16 MiB.
CREATE TABLE delivery_renders (
    delivery_id uuid PRIMARY KEY REFERENCES deliveries (id) ON DELETE CASCADE,
    manifests bytea NOT NULL CHECK (length(manifests) <= 16777216),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- What an environment holds: the objects its last whole apply applied, in the order it applied them, with the UID each
-- had then, and what an apply that failed partway, or a prune, left; each with the delivery whose apply recorded it and
-- its health as last read. Pruning and teardown delete from it.
CREATE TABLE environment_objects (
    environment_id uuid NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    api_group text NOT NULL,
    version text NOT NULL,
    kind text NOT NULL,
    namespace text NOT NULL,
    name text NOT NULL,
    uid text NOT NULL,
    delivery_id uuid REFERENCES deliveries (id) ON DELETE SET NULL,
    health text NOT NULL DEFAULT 'unknown' CHECK (health IN ('healthy', 'progressing', 'unknown', 'missing', 'degraded')),
    message text NOT NULL DEFAULT '',
    PRIMARY KEY (environment_id, position)
);

-- The controller claims a queued delivery for a while, and puts off one that waits for room.
CREATE INDEX deliveries_claimable ON deliveries (coalesce(lease_until, queued_at)) WHERE state = 'queued';
-- It follows the deliveries that render and apply.
CREATE INDEX deliveries_working ON deliveries (org_id) WHERE state IN ('rendering', 'applying');

-- +goose Down
-- Rolling back forgets the inventories, so a later apply can't prune what earlier ones applied: what they applied stays
-- in its clusters, labelled.
DROP INDEX deliveries_working;
DROP INDEX deliveries_claimable;
DROP TABLE environment_objects;
DROP TABLE delivery_renders;
DROP TABLE delivery_tokens;
