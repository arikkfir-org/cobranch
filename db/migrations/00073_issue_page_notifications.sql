-- Live issue pages: a branch linked to an issue, or unlinked from it, shows in the issue lists and on the issue's page,
-- so a change to branch_issues notifies channel cobranch_changes with the organization it belongs to, through 00066's
-- notify_changes (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/console.md, "Live pages").
-- The rest of what those pages show (the issue, its events, its tasks and their branches) changes with rows whose
-- triggers 00069 adds.

-- +goose Up

CREATE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON branch_issues
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');

-- +goose Down

DROP TRIGGER notify_changes ON branch_issues;
