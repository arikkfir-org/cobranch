-- Agent tasks' pods: what the controller keeps of each task it takes, which it runs in a pod of its organization's job
-- namespace (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md, "The agent's pod").
-- +goose Up

-- How long a controller's claim of a queued task holds, or until when a task the quota refused waits; how many pods the
-- task has had, the first and those started again after one died; and when the newest started. Only the controller
-- writes it, so it isn't audited: the task's states and transcript are its record.
CREATE TABLE agent_task_pods (
    task_id uuid PRIMARY KEY REFERENCES agent_tasks (id) ON DELETE CASCADE,
    lease_until timestamptz,
    attempt integer NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    pod_started_at timestamptz
);

-- +goose Down
DROP TABLE agent_task_pods;
