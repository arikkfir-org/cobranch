-- The tokens of internal calls between Cobranch's own workloads: the REST and GraphQL servers' calls to the Git server
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/code-browsing.md, "The internal call").
-- +goose Up

-- An internal token is a secret both ends of an internal call read from here, so that a render of the chart without
-- the cluster, which can't keep a Secret it generates, needs nothing from the operator. The Git server's comes from
-- PostgreSQL's random UUIDs, 244 random bits; rotating it is an UPDATE of its row and a restart of every server.
CREATE TABLE internal_tokens (
    name text PRIMARY KEY,
    token text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO internal_tokens (name, token)
VALUES ('git-server', replace(gen_random_uuid()::text || gen_random_uuid()::text, '-', ''));

-- +goose Down
DROP TABLE internal_tokens;
