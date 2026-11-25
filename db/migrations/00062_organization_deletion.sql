-- Deleting an organization: an Owner marks it Deleting, which frees its slugs and ends everything of it at once, and
-- maintenance purges its rows once its environments' teardowns end
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/sign-in-and-organizations.md, "Deleting
-- one"). Its version is its ETag, which the deletion names.
-- +goose Up
ALTER TABLE organizations ADD COLUMN version bigint NOT NULL DEFAULT 1;
CREATE TRIGGER bump_version BEFORE UPDATE ON organizations FOR EACH ROW EXECUTE FUNCTION bump_version();

-- When it became Deleting, and who made it so; NULL for a live organization. deleted_by stays NULL once they are gone.
ALTER TABLE organizations ADD COLUMN deleting_at timestamptz;
ALTER TABLE organizations ADD COLUMN deleted_by uuid REFERENCES principals (id) ON DELETE SET NULL;
ALTER TABLE organizations ADD CONSTRAINT organizations_deleted_by CHECK (deleting_at IS NOT NULL OR deleted_by IS NULL);
CREATE INDEX organizations_deleting ON organizations (deleting_at) WHERE deleting_at IS NOT NULL;

-- A slug is unique among live organizations: one being deleted leaves its slug to a new one at once.
ALTER TABLE organizations DROP CONSTRAINT organizations_slug_key;
CREATE UNIQUE INDEX organizations_live_slug ON organizations (slug) WHERE deleting_at IS NULL;

-- +goose Down
-- Rolling back purges the organizations being deleted at once, which their environments' teardowns may not have
-- finished; the audit log keeps who deleted them.
DELETE FROM events e USING organizations o WHERE e.org_id = o.id AND o.deleting_at IS NOT NULL;
DELETE FROM organizations WHERE deleting_at IS NOT NULL;
DROP INDEX organizations_live_slug;
ALTER TABLE organizations ADD CONSTRAINT organizations_slug_key UNIQUE (slug);
DROP INDEX organizations_deleting;
ALTER TABLE organizations DROP CONSTRAINT organizations_deleted_by;
ALTER TABLE organizations DROP COLUMN deleted_by;
ALTER TABLE organizations DROP COLUMN deleting_at;
DROP TRIGGER bump_version ON organizations;
ALTER TABLE organizations DROP COLUMN version;
