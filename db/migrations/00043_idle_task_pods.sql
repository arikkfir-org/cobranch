-- Idle agent pods: the controller stops the pod of a task that has waited agents.idle for an answer, and starts its
-- next once someone answers (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md, "The
-- agent's pod"). stopped_at is when it stopped the task's pod, while it waits; stops how many times it did, which
-- aren't deaths, so they don't count towards the task's most pods.
-- +goose Up
ALTER TABLE agent_task_pods
    ADD COLUMN stopped_at timestamptz,
    ADD COLUMN stops integer NOT NULL DEFAULT 0 CHECK (stops >= 0);

-- +goose Down
ALTER TABLE agent_task_pods DROP COLUMN stops, DROP COLUMN stopped_at;
