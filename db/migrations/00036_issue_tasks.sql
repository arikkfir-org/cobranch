-- Issues handed to agents: assigning an issue to an agent gives it a task of kind 'issue' that names the issue
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/issues.md, "Agents"). A deleted issue
-- leaves its tasks, which name none then.
-- +goose Up
INSERT INTO task_kinds (kind) VALUES ('issue') ON CONFLICT DO NOTHING;
ALTER TABLE agent_tasks ADD COLUMN issue_id uuid REFERENCES issues (id) ON DELETE SET NULL;
CREATE INDEX agent_tasks_issue ON agent_tasks (issue_id, created_at) WHERE issue_id IS NOT NULL;

-- +goose Down
-- Rolling back deletes the issues' tasks, which no other kind names; the audit log keeps what they did.
DELETE FROM agent_tasks WHERE kind = 'issue';
DROP INDEX agent_tasks_issue;
ALTER TABLE agent_tasks DROP COLUMN issue_id;
DELETE FROM task_kinds WHERE kind = 'issue';
