-- Live organization pages: the Overview, Repositories, Activity, Agents, Artifacts and Settings · Usage, and the
-- sidebar's counts, follow what their rows say, so a change to one of these tables notifies channel cobranch_changes
-- with the organization it belongs to, through 00066's notify_changes
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/console.md, "Live pages").

-- +goose Up

CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON repositories
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON branches
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON annotations
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON pushes
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON reviews
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON issues
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON events
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON agent_tasks
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON task_spend
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON job_runs
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON deliveries
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON artifacts
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON packs
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON organization_limits
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');

-- +goose Down

DROP TRIGGER notify_changes ON organization_limits;
DROP TRIGGER notify_changes ON packs;
DROP TRIGGER notify_changes ON artifacts;
DROP TRIGGER notify_changes ON deliveries;
DROP TRIGGER notify_changes ON job_runs;
DROP TRIGGER notify_changes ON task_spend;
DROP TRIGGER notify_changes ON agent_tasks;
DROP TRIGGER notify_changes ON events;
DROP TRIGGER notify_changes ON issues;
DROP TRIGGER notify_changes ON reviews;
DROP TRIGGER notify_changes ON pushes;
DROP TRIGGER notify_changes ON annotations;
DROP TRIGGER notify_changes ON branches;
DROP TRIGGER notify_changes ON repositories;
