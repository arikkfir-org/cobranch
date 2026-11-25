-- The role the servers work as inside one organization. A transaction that names its organization switches to
-- cobranch_tenant for its length (pin_organization: SET LOCAL ROLE, with the organization in session variable
-- cobranch.org_id), so the tables' row-level security policies bind it; work that spans organizations stays the role
-- Cobranch connects as, which owns the tables and so is bound by none
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/tenant-isolation.md). The role can't log
-- in and holds nothing but the tables' rows: the connecting role lends it its place for a transaction at a time.
--
-- Roles belong to the server, not the database, so another Cobranch database there may have made it already; and the
-- connecting role may be one that can't make roles, when its operator made cobranch_tenant for it.

-- +goose Up

-- +goose StatementBegin
DO $$
DECLARE
    lend text := 'GRANT cobranch_tenant TO ' || quote_ident(current_user) || '; then migrate again.';
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'cobranch_tenant') THEN
        BEGIN
            CREATE ROLE cobranch_tenant NOLOGIN;
        EXCEPTION
            -- Another database's migration made it between the check and here.
            WHEN duplicate_object OR unique_violation THEN NULL;
            WHEN insufficient_privilege THEN
                RAISE EXCEPTION 'role % may not create role cobranch_tenant', current_user
                    USING HINT = 'As a role that may, run: CREATE ROLE cobranch_tenant NOLOGIN; ' || lend;
        END;
    END IF;
    -- A superuser switches to any role; any other needs to be a member of it.
    IF NOT pg_has_role(current_user, 'cobranch_tenant', 'SET') THEN
        BEGIN
            EXECUTE format('GRANT cobranch_tenant TO %I', current_user);
        EXCEPTION WHEN insufficient_privilege THEN
            RAISE EXCEPTION 'role % may not work as role cobranch_tenant', current_user
                USING HINT = 'As a role that may, run: ' || lend;
        END;
    END IF;
END
$$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA public TO cobranch_tenant;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO cobranch_tenant;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO cobranch_tenant;
-- Every table a later migration creates.
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO cobranch_tenant;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE ON SEQUENCES TO cobranch_tenant;

-- +goose StatementBegin
-- current_org is the organization the transaction is pinned to, or NULL in one that named none.
CREATE FUNCTION current_org() RETURNS uuid LANGUAGE sql STABLE AS $$
    SELECT nullif(current_setting('cobranch.org_id', true), '')::uuid
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- pin_organization pins the transaction to one organization: until it ends, it works as cobranch_tenant, which sees
-- and changes that organization's rows alone.
CREATE FUNCTION pin_organization(org uuid) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    PERFORM set_config('cobranch.org_id', org::text, true);
    PERFORM set_config('role', 'cobranch_tenant', true);
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- pin_organization_of pins the transaction to the organization that owns the row with row_id of tbl, a table with
-- columns id and org_id: for work that starts from a repository, a task or another row rather than its organization.
-- A row that isn't there pins no organization, so the transaction finds nothing.
CREATE FUNCTION pin_organization_of(tbl regclass, row_id uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    org uuid;
BEGIN
    EXECUTE format('SELECT org_id FROM %s WHERE id = $1', tbl) INTO org USING row_id;
    PERFORM pin_organization(coalesce(org, '00000000-0000-0000-0000-000000000000'));
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION pin_organization_of(regclass, uuid);
DROP FUNCTION pin_organization(uuid);
DROP FUNCTION current_org();
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE USAGE ON SEQUENCES FROM cobranch_tenant;
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM cobranch_tenant;
REVOKE USAGE ON ALL SEQUENCES IN SCHEMA public FROM cobranch_tenant;
REVOKE SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public FROM cobranch_tenant;
REVOKE USAGE ON SCHEMA public FROM cobranch_tenant;
-- The role stays: it is the server's, and another database's Cobranch may use it.
