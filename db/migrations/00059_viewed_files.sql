-- Viewed files: the files of a branch's change a person marked Viewed on its Files changed tab, each at the object
-- its content was when they did, so that a file changed since shows unviewed again, and the marks follow them to
-- every browser (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/reviews.md, "The Console").
-- +goose Up

-- A principal's mark on a file of a branch of the organization's: its path, and the object of its content when they
-- marked it, "" for a file the branch deletes. A branch deleted keeps its row, and its marks with it; the name living
-- again is another branch, without them.
CREATE TABLE viewed_files (
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    principal_id uuid NOT NULL REFERENCES principals (id) ON DELETE CASCADE,
    branch_id uuid NOT NULL REFERENCES branches (id) ON DELETE CASCADE,
    path text NOT NULL CHECK (path <> ''),
    object text NOT NULL CHECK (object ~ '^([0-9a-f]{40}|[0-9a-f]{64})?$'),
    viewed_at timestamptz NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    PRIMARY KEY (principal_id, branch_id, path)
);
CREATE INDEX viewed_files_branch ON viewed_files (branch_id);

CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON viewed_files FOR EACH ROW EXECUTE FUNCTION audit();
CREATE TRIGGER bump_version BEFORE UPDATE ON viewed_files FOR EACH ROW EXECUTE FUNCTION bump_version();

-- +goose Down
-- Rolling back forgets every Viewed mark, which people set again; the audit log keeps them.
DROP TABLE viewed_files;
