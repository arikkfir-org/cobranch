-- Live settings: each change to a row that an organization's settings pages show notifies channel cobranch_settings
-- with the organization as its transaction commits, so the servers that follow those pages read them again
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md, "Live settings"). Settings ·
-- Models shows the providers, the secrets that hold their keys, the models and the agents on them.

-- +goose Up

-- +goose StatementBegin
-- notify_settings notifies cobranch_settings with the organization of the row before the change and after it:
-- PostgreSQL sends each payload once per transaction, after it commits.
CREATE FUNCTION notify_settings() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        PERFORM pg_notify('cobranch_settings', OLD.org_id::text);
    END IF;
    IF TG_OP <> 'DELETE' THEN
        PERFORM pg_notify('cobranch_settings', NEW.org_id::text);
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER notify_settings AFTER INSERT OR UPDATE OR DELETE ON model_providers
    FOR EACH ROW EXECUTE FUNCTION notify_settings();
CREATE TRIGGER notify_settings AFTER INSERT OR UPDATE OR DELETE ON models
    FOR EACH ROW EXECUTE FUNCTION notify_settings();
CREATE TRIGGER notify_settings AFTER INSERT OR UPDATE OR DELETE ON agents
    FOR EACH ROW EXECUTE FUNCTION notify_settings();
CREATE TRIGGER notify_settings AFTER INSERT OR UPDATE OR DELETE ON secrets
    FOR EACH ROW EXECUTE FUNCTION notify_settings();

-- +goose Down

DROP TRIGGER notify_settings ON secrets;
DROP TRIGGER notify_settings ON agents;
DROP TRIGGER notify_settings ON models;
DROP TRIGGER notify_settings ON model_providers;
DROP FUNCTION notify_settings();
