-- Auditing: every change to an audited table is a row of audit_log, written by trigger audit() with the principal the
-- transaction acts for, which the store sets in session variable cobranch.principal_id (set_config, local to the
-- transaction). A row without a principal is Cobranch's own doing: a migration, a seed or maintenance.

-- +goose Up

CREATE TABLE audit_log (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    at timestamptz NOT NULL DEFAULT now(),
    transaction_id xid8 NOT NULL DEFAULT pg_current_xact_id(),
    table_name text NOT NULL,
    action text NOT NULL CHECK (action IN ('insert', 'update', 'delete')),
    principal_id uuid,
    -- The row before the change (an update or a delete), whole.
    before jsonb,
    -- The row after the change: whole after an insert, only the columns that changed after an update.
    after jsonb,
    CHECK (before IS NOT NULL OR after IS NOT NULL)
);
CREATE INDEX audit_log_table ON audit_log (table_name, at);
CREATE INDEX audit_log_principal ON audit_log (principal_id, at);

-- +goose StatementBegin
-- audit records the row change that fired it. Its arguments name columns to leave out of the log, such as a token's
-- hash. An update that changes nothing records nothing.
CREATE FUNCTION audit() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    principal uuid := nullif(current_setting('cobranch.principal_id', true), '')::uuid;
    before jsonb;
    after jsonb;
    hidden text;
BEGIN
    IF TG_OP = 'INSERT' THEN
        after := to_jsonb(NEW);
    ELSIF TG_OP = 'DELETE' THEN
        before := to_jsonb(OLD);
    ELSE
        before := to_jsonb(OLD);
        SELECT jsonb_object_agg(n.key, n.value) INTO after
        FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM before -> n.key;
        IF after IS NULL THEN
            RETURN NULL;
        END IF;
    END IF;
    FOREACH hidden IN ARRAY coalesce(TG_ARGV, '{}') LOOP
        before := before - hidden;
        after := after - hidden;
    END LOOP;
    INSERT INTO audit_log (table_name, action, principal_id, before, after)
    VALUES (TG_TABLE_NAME, lower(TG_OP), principal, before, after);
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON principals FOR EACH ROW EXECUTE FUNCTION audit();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON principal_emails FOR EACH ROW EXECUTE FUNCTION audit();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON identities FOR EACH ROW EXECUTE FUNCTION audit();
-- A session's use (last_seen_at, idle_expires_at) is not a change worth a row; its making, revocation and deletion are.
CREATE TRIGGER audit AFTER INSERT OR UPDATE OF revoked_at OR DELETE ON sessions
    FOR EACH ROW EXECUTE FUNCTION audit('token_hash');
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON roles FOR EACH ROW EXECUTE FUNCTION audit();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON grants FOR EACH ROW EXECUTE FUNCTION audit();

-- +goose Down
DROP TRIGGER audit ON grants;
DROP TRIGGER audit ON roles;
DROP TRIGGER audit ON sessions;
DROP TRIGGER audit ON identities;
DROP TRIGGER audit ON principal_emails;
DROP TRIGGER audit ON principals;
DROP FUNCTION audit();
DROP TABLE audit_log;
