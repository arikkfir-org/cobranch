-- Live repository pages: the tables a repository's code and CI pages show name the repository as well as the
-- organization when a row changes, so that a repository's followers wake for its own changes alone
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/console.md, "Live pages"). 00069's
-- triggers on them named only the organization, which its pages follow.

-- +goose Up

CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON branches
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id', 'repo_id');
CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON annotations
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id', 'repo_id');
CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON pushes
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id', 'repo_id');
CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON reviews
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id', 'repo_id');
CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON job_runs
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id', 'repo_id');
CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON deliveries
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id', 'repo_id');
CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON packs
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id', 'repo_id');

-- +goose Down

CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON packs
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON deliveries
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON job_runs
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON reviews
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON pushes
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON annotations
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON branches
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
