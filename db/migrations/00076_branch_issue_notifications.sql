-- Live branch pages: a branch's page shows the issue the branch works on, and follows the branch with its repository,
-- so linking a branch to an issue, or unlinking it, names the branch's repository on channel cobranch_changes as well
-- as its organization, which 00073 named alone
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/console.md, "Live pages"). branch_issues
-- carries no repo_id, so its trigger reads the branch's.

-- +goose Up

-- +goose StatementBegin
-- notify_branch_issue_changes notifies cobranch_changes with the organization of the link that changed and the
-- repository of its branch. A link deleted with its branch names the organization alone: the branch's own row named
-- the repository.
CREATE FUNCTION notify_branch_issue_changes() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    repo uuid;
BEGIN
    PERFORM pg_notify('cobranch_changes', COALESCE(NEW.org_id, OLD.org_id)::text);
    SELECT b.repo_id INTO repo FROM branches b WHERE b.id = COALESCE(NEW.branch_id, OLD.branch_id);
    IF repo IS NOT NULL THEN
        PERFORM pg_notify('cobranch_changes', repo::text);
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON branch_issues
    FOR EACH ROW EXECUTE FUNCTION notify_branch_issue_changes();

-- +goose Down

CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON branch_issues
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
DROP FUNCTION notify_branch_issue_changes();
