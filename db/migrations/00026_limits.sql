-- Quotas: how many organizations a user may create, how many repositories an organization may hold and how many bytes
-- of Git data it may store (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/quotas.md). A
-- limit is a number of 0 or more, 0 capping nothing; NULL takes the next source: an organization's own limit, else the
-- installation's, else the built-in default. Usage is counted when asked, from the rows themselves.
-- +goose Up

-- The installation's defaults, beside its other settings, versioned and audited with them.
ALTER TABLE installation_settings
    ADD COLUMN organizations_per_user bigint CHECK (organizations_per_user >= 0),
    ADD COLUMN repositories_per_organization bigint CHECK (repositories_per_organization >= 0),
    ADD COLUMN git_data_per_organization bigint CHECK (git_data_per_organization >= 0);

-- An organization's own limits, which an administrator sets; no row, or a NULL limit, takes the installation's. Every
-- change bumps version, which a change names to refuse one made meanwhile, and is audited.
CREATE TABLE organization_limits (
    org_id uuid PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    repositories bigint CHECK (repositories >= 0),
    git_data bigint CHECK (git_data >= 0),
    version bigint NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES principals (id) ON DELETE SET NULL
);
CREATE TRIGGER bump_version BEFORE UPDATE ON organization_limits FOR EACH ROW EXECUTE FUNCTION bump_version();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON organization_limits FOR EACH ROW EXECUTE FUNCTION audit();

-- The user who created an organization for themselves, against whom it counts; NULL for one an administrator created
-- for its first Owner in the Back-office, which counts against nobody. Organizations made before this migration take
-- it from their org.created event, which names an owner only when an administrator made it.
ALTER TABLE organizations ADD COLUMN created_by uuid REFERENCES principals (id) ON DELETE SET NULL;
UPDATE organizations o SET created_by = e.actor_id
FROM events e
WHERE e.kind = 'org.created' AND e.subject_id = o.id AND NOT e.details ? 'owner';
CREATE INDEX organizations_created_by ON organizations (created_by);

-- An organization's Git data is the sum of its repositories' active packs, summed in each push's transaction.
CREATE INDEX packs_active_org ON packs (org_id) INCLUDE (size) WHERE state = 'active';

-- +goose Down
-- Rolling back forgets every limit an administrator set and who created each organization.
DROP INDEX packs_active_org;
DROP INDEX organizations_created_by;
ALTER TABLE organizations DROP COLUMN created_by;
DROP TABLE organization_limits;
ALTER TABLE installation_settings
    DROP COLUMN organizations_per_user,
    DROP COLUMN repositories_per_organization,
    DROP COLUMN git_data_per_organization;
