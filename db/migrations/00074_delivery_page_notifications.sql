-- Live delivery pages: an environment, and each object it holds, names its organization and its repository on
-- channel cobranch_changes (00066) as its transaction commits, so that the Delivery page, an environment's page and a
-- branch's Environment tab read them again as a rollout progresses
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/console.md, "Live pages"). Its deliveries
-- name them since 00071. An object's health changes without its environment's while a rollout runs (2/3 ready, then
-- 3/3), so the objects notify too, through their environment, since they carry neither.

-- +goose Up

CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON environments
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id', 'repo_id');

-- +goose StatementBegin
-- notify_environment_object_changes notifies cobranch_changes with the organization and the repository of the
-- environment whose object changed. An object deleted with its environment names nothing: the environment's own row
-- notified.
CREATE FUNCTION notify_environment_object_changes() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    org uuid;
    repo uuid;
BEGIN
    SELECT e.org_id, e.repo_id INTO org, repo FROM environments e WHERE e.id = COALESCE(NEW.environment_id, OLD.environment_id);
    IF org IS NOT NULL THEN
        PERFORM pg_notify('cobranch_changes', org::text);
        PERFORM pg_notify('cobranch_changes', repo::text);
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON environment_objects
    FOR EACH ROW EXECUTE FUNCTION notify_environment_object_changes();

-- +goose Down

DROP TRIGGER notify_changes ON environment_objects;
DROP FUNCTION notify_environment_object_changes();
DROP TRIGGER notify_changes ON environments;
