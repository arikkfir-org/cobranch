-- A task that failed on its agent's cap says so, so that it still counts as one once the cap is raised, as it usually is
-- next (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md, "Spend and caps").
-- +goose Up
ALTER TABLE agent_tasks ADD COLUMN cap_reached boolean NOT NULL DEFAULT false;

-- The tasks that already failed on their cap say why in the words Cobranch wrote then.
UPDATE agent_tasks SET cap_reached = true WHERE state = 'failed' AND reason LIKE 'It spent $%, reaching its cap of $% a task.';

-- +goose Down
ALTER TABLE agent_tasks DROP COLUMN cap_reached;
