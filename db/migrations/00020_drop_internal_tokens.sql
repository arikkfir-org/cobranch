-- The internal call reads as the viewer whose token the REST and GraphQL servers send on, and the Git server checks
-- what that viewer may read, so the token both ends shared (00014_internal_tokens.sql) goes
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/code-browsing.md, "The internal call").
-- IF EXISTS, because a database migrated by a build of this change before it was renumbered past 00019_api_merges
-- has dropped the table already.
-- +goose Up
DROP TABLE IF EXISTS internal_tokens;

-- +goose Down
CREATE TABLE internal_tokens (
    name text PRIMARY KEY,
    token text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO internal_tokens (name, token)
VALUES ('git-server', replace(gen_random_uuid()::text || gen_random_uuid()::text, '-', ''));
