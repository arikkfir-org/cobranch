-- Branch deletions: who deleted a branch, by a push or by asking Cobranch, beside when
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/branch-status.md, "Deleting a branch").
-- A deleted branch keeps its row, so its merge, its commits' annotations and its history stay; the Console reads
-- the latest deletion of a name.
-- +goose Up
ALTER TABLE branches ADD COLUMN deleted_by uuid REFERENCES principals (id) ON DELETE SET NULL;
CREATE INDEX branches_deleted ON branches (repo_id, name, deleted_at DESC) WHERE deleted_at IS NOT NULL;

-- +goose Down
-- Rolling back forgets who deleted each branch; when stays.
DROP INDEX branches_deleted;
ALTER TABLE branches DROP COLUMN deleted_by;
