-- The Back-office: an installation's administrator who enters an organization is granted its Owner role with source
-- entry, so its audit trail and a later review tell that grant from one a person made
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/back-office.md, "Entering one").
-- +goose Up
ALTER TABLE grants DROP CONSTRAINT grants_source_check;
ALTER TABLE grants ADD CONSTRAINT grants_source_check CHECK (source IN ('grant', 'admins', 'self_serve', 'entry'));

-- +goose Down
-- The previous schema has no room for an entry's grant: it becomes an ordinary one.
UPDATE grants SET source = 'grant' WHERE source = 'entry';
ALTER TABLE grants DROP CONSTRAINT grants_source_check;
ALTER TABLE grants ADD CONSTRAINT grants_source_check CHECK (source IN ('grant', 'admins', 'self_serve'));
