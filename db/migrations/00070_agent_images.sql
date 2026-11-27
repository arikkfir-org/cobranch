-- The image an organization's agent runs in, which its settings page names
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/repo-agents.md, "The image").
-- +goose Up

-- An image reference, such as ghcr.io/acme/tools:1.4; empty for the installation's agents.image.
ALTER TABLE agents ADD COLUMN image text NOT NULL DEFAULT '' CHECK (char_length(image) <= 512);

-- +goose Down
-- Rolling back forgets the images; every agent runs in the installation's image again.
ALTER TABLE agents DROP COLUMN image;
