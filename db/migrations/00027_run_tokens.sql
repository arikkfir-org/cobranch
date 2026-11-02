-- Run tokens: the credential each run of a job acts with, as its job, while it runs
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "Who a run is"). The controller
-- makes one as it starts the run, and deletes it as the run ends.
-- +goose Up

-- A run's token, by its hash: a credential of kind run_token, which acts as the run's job, may do what the job
-- declared on the run's repository, and reads its organization, until it expires with the run's timeout. Runs are
-- history the controller writes, and their tokens with them, so neither is audited.
CREATE TABLE run_tokens (
    run_id uuid PRIMARY KEY REFERENCES job_runs (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    hint text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    last_used_at timestamptz
);
CREATE INDEX run_tokens_expires ON run_tokens (expires_at);

-- The controller claims a queued run for a while, and puts off one that waits for room.
CREATE INDEX job_runs_claimable ON job_runs (coalesce(lease_until, queued_at)) WHERE state = 'queued';
-- It follows the runs that run.
CREATE INDEX job_runs_running ON job_runs (org_id) WHERE state = 'running';

-- +goose Down
DROP INDEX job_runs_running;
DROP INDEX job_runs_claimable;
DROP TABLE run_tokens;
