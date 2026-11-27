-- Live organization settings: the rows Settings' Agents, Members, Roles and API keys show notify channel
-- cobranch_changes (00066) with their organization as their transaction commits, so the servers that follow those
-- pages read them again (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md, "Live
-- settings"). A grant names its organization through its scope, and a repository grant its repository too; an API
-- key's secret names its key's organization.

-- +goose Up

CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON memberships
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON invitations
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON join_requests
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
-- A built-in role carries no organization and changes only in a migration.
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON roles
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
-- An API key is the one principal with an organization; a person's name shows on the next read.
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON principals
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');

-- +goose StatementBegin
-- notify_grant_changes notifies cobranch_changes with the organization of a grant before the change and after it: its
-- scope's own ID at an organization; the organization of the repository, secret or artifact it is on, and at a
-- repository the repository too. A grant at the installation names no organization.
CREATE FUNCTION notify_grant_changes() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    g grants;
    org uuid;
BEGIN
    FOREACH g IN ARRAY ARRAY[OLD, NEW] LOOP
        CONTINUE WHEN g IS NULL OR g.scope_id IS NULL;
        org := NULL;
        CASE g.scope
            WHEN 'organization' THEN
                org := g.scope_id;
            WHEN 'repository' THEN
                PERFORM pg_notify('cobranch_changes', g.scope_id::text);
                SELECT r.org_id INTO org FROM repositories r WHERE r.id = g.scope_id;
            WHEN 'secret' THEN
                SELECT s.org_id INTO org FROM secrets s WHERE s.id = g.scope_id;
            WHEN 'artifact' THEN
                SELECT a.org_id INTO org FROM artifacts a WHERE a.id = g.scope_id;
            ELSE
                NULL;
        END CASE;
        IF org IS NOT NULL THEN
            PERFORM pg_notify('cobranch_changes', org::text);
        END IF;
    END LOOP;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON grants
    FOR EACH ROW EXECUTE FUNCTION notify_grant_changes();

-- +goose StatementBegin
-- notify_credential_changes notifies cobranch_changes with the organization of the API key whose secret changed: one
-- made, revoked, used or deleted. A secret deleted with its key names nothing: the key's own row notified.
CREATE FUNCTION notify_credential_changes() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    org uuid;
BEGIN
    SELECT p.org_id INTO org FROM principals p WHERE p.id = COALESCE(NEW.principal_id, OLD.principal_id);
    IF org IS NOT NULL THEN
        PERFORM pg_notify('cobranch_changes', org::text);
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON credentials
    FOR EACH ROW EXECUTE FUNCTION notify_credential_changes();

-- +goose Down

DROP TRIGGER notify_changes ON credentials;
DROP FUNCTION notify_credential_changes();
DROP TRIGGER notify_changes ON grants;
DROP FUNCTION notify_grant_changes();
DROP TRIGGER notify_changes ON principals;
DROP TRIGGER notify_changes ON roles;
DROP TRIGGER notify_changes ON join_requests;
DROP TRIGGER notify_changes ON invitations;
DROP TRIGGER notify_changes ON memberships;
