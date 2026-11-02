-- Task tokens and transcripts: the token a task's pod acts with, as its agent capped by its assigner, and the steps its
-- harness records as it works (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md,
-- "The agent's pod" and "The transcript").
-- +goose Up

-- A task token, hashed: Cobranch issues one for an hour whenever it renews a running task's, so a task may hold two at a
-- time while its pod picks up the newer. A token acts only while its task hasn't ended. Cobranch alone makes and sweeps
-- them, so they aren't audited.
CREATE TABLE agent_task_tokens (
    token_hash bytea PRIMARY KEY,
    task_id uuid NOT NULL REFERENCES agent_tasks (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX agent_task_tokens_task ON agent_task_tokens (task_id);
CREATE INDEX agent_task_tokens_expires ON agent_task_tokens (expires_at);

-- A step of a task, numbered from 1 in its task: the instructions it began with, a model turn, a tool's result, or a
-- change of its state. Only the task's own token appends, each entry once, and nobody changes one, so they aren't
-- audited: the transcript is the record.
CREATE TABLE agent_task_entries (
    task_id uuid NOT NULL REFERENCES agent_tasks (id) ON DELETE CASCADE,
    seq integer NOT NULL CHECK (seq >= 1),
    kind text NOT NULL CHECK (kind IN ('input', 'turn', 'result', 'state')),
    body jsonb NOT NULL CHECK (jsonb_typeof(body) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, seq)
);

-- +goose Down
DROP TABLE agent_task_entries;
DROP TABLE agent_task_tokens;
