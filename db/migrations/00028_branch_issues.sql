-- Branches linked to issues: a branch works on at most one issue, linked by its name or by hand
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/issues.md, "Branches").
-- +goose Up

-- A branch's link to its issue, keyed by the branch, so a branch has at most one. linked_by says how: by its name,
-- which the push worker and an issue's creation make, or by hand, which nothing undoes. A row without an issue is a
-- branch unlinked by hand, which its name never links again. Deleting the issue deletes its links; deleting the
-- branch keeps its row, as it keeps the branch's.
CREATE TABLE branch_issues (
    branch_id uuid PRIMARY KEY REFERENCES branches (id) ON DELETE CASCADE,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    issue_id uuid REFERENCES issues (id) ON DELETE CASCADE,
    linked_by text NOT NULL CHECK (linked_by IN ('name', 'hand')),
    linked_at timestamptz NOT NULL DEFAULT now(),
    CHECK (issue_id IS NOT NULL OR linked_by = 'hand')
);
CREATE INDEX branch_issues_issue ON branch_issues (issue_id);

CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON branch_issues FOR EACH ROW EXECUTE FUNCTION audit();

-- +goose Down
-- Rolling back forgets every link; the audit log and the issues' activity keep what they were.
DROP TABLE branch_issues;
