-- Run steps: each of a run's pod's containers, in the order they run, as they stood when the run ended, so that its page
-- still shows them once the pod is gone (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md,
-- "Steps").
-- +goose Up

-- A container of a run's pod at position, counting from 0 in the order they run: the checkout's, the job's init
-- containers', then its containers'. The controller writes them once, as it ends the run, as it does the run's logs:
-- history, not data people manage, so not audited. exit_code is 0 until it ended.
CREATE TABLE job_run_steps (
    run_id uuid NOT NULL REFERENCES job_runs (id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    name text NOT NULL CHECK (name <> ''),
    state text NOT NULL CHECK (state IN ('waiting', 'running', 'succeeded', 'failed')),
    exit_code integer NOT NULL DEFAULT 0,
    reason text NOT NULL DEFAULT '',
    started_at timestamptz,
    ended_at timestamptz,
    PRIMARY KEY (run_id, position),
    UNIQUE (run_id, name)
);

-- +goose Down
-- Rolling back forgets the ended runs' steps; their logs stay.
DROP TABLE job_run_steps;
