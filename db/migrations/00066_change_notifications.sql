-- Live pages: each change to a row that a Console page shows notifies channel cobranch_changes, naming what the row
-- belongs to, as its transaction commits, so the servers that follow those pages read them again
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md, "Live settings"). Settings ·
-- Models comes first: the providers, the secrets that hold their keys, the models and the agents on them, each named by
-- its organization. Other pages add their tables with the columns that name what their followers watch.

-- +goose Up

-- +goose StatementBegin
-- notify_changes notifies cobranch_changes with the ID in each column TG_ARGV names, of the row before the change and
-- after it: PostgreSQL sends each payload once per transaction, after it commits.
CREATE FUNCTION notify_changes() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    col text;
    named text;
BEGIN
    FOREACH col IN ARRAY TG_ARGV LOOP
        FOREACH named IN ARRAY ARRAY[to_jsonb(OLD) ->> col, to_jsonb(NEW) ->> col] LOOP
            IF named IS NOT NULL THEN
                PERFORM pg_notify('cobranch_changes', named);
            END IF;
        END LOOP;
    END LOOP;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON model_providers
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON models
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON agents
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON secrets
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');

-- +goose Down

DROP TRIGGER notify_changes ON secrets;
DROP TRIGGER notify_changes ON agents;
DROP TRIGGER notify_changes ON models;
DROP TRIGGER notify_changes ON model_providers;
DROP FUNCTION notify_changes();
