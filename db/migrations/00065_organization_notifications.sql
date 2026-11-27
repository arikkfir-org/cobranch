-- Live organization lists: each change to a row that shapes the organizations someone belongs to, or what they may do
-- there, notifies channel cobranch_organizations as its transaction commits, so the servers that follow those lists
-- read them again (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/sign-in-and-organizations.md,
-- "Live organization lists"). A membership or a grant names its principal; an organization or one of its custom roles
-- names the organization, which its members' followers watch.

-- +goose Up

-- +goose StatementBegin
-- notify_organizations notifies cobranch_organizations with the ID in the column TG_ARGV[0] names, of the row before
-- the change and after it: PostgreSQL sends each payload once per transaction, after it commits.
CREATE FUNCTION notify_organizations() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    named text;
BEGIN
    FOREACH named IN ARRAY ARRAY[to_jsonb(OLD) ->> TG_ARGV[0], to_jsonb(NEW) ->> TG_ARGV[0]] LOOP
        IF named IS NOT NULL THEN
            PERFORM pg_notify('cobranch_organizations', named);
        END IF;
    END LOOP;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER notify_organizations AFTER INSERT OR UPDATE OR DELETE ON memberships
    FOR EACH ROW EXECUTE FUNCTION notify_organizations('user_id');
CREATE TRIGGER notify_organizations AFTER INSERT OR UPDATE OR DELETE ON grants
    FOR EACH ROW EXECUTE FUNCTION notify_organizations('principal_id');
CREATE TRIGGER notify_organizations AFTER UPDATE OR DELETE ON organizations
    FOR EACH ROW EXECUTE FUNCTION notify_organizations('id');
-- A built-in role, which carries no organization, changes only in a migration.
CREATE TRIGGER notify_organizations AFTER UPDATE OR DELETE ON roles
    FOR EACH ROW EXECUTE FUNCTION notify_organizations('org_id');

-- +goose Down

DROP TRIGGER notify_organizations ON roles;
DROP TRIGGER notify_organizations ON organizations;
DROP TRIGGER notify_organizations ON grants;
DROP TRIGGER notify_organizations ON memberships;
DROP FUNCTION notify_organizations();
