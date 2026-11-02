-- Re-runs and schedules: a member re-runs an ended run as the next attempt of its definition at its commit, and the
-- controller queues each schedule slot once (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md,
-- "Triggers" and "A run's life").
-- +goose Up

-- A re-run names the run its attempts started from, and counts its attempt from there: the first run is attempt 1.
ALTER TABLE job_runs ADD COLUMN attempt integer NOT NULL DEFAULT 1 CHECK (attempt >= 1);
ALTER TABLE job_runs ADD COLUMN rerun_of uuid REFERENCES job_runs (id) ON DELETE SET NULL;
CREATE INDEX job_runs_rerun_of ON job_runs (rerun_of) WHERE rerun_of IS NOT NULL;

-- The schedule slots the controller queued a job's runs for, by the job's name in its repository: a slot is queued once,
-- whichever controller replica gets to it first. The controller deletes a day's old slots. Like runs, they are
-- history Cobranch writes, not data people manage, so they aren't audited.
CREATE TABLE schedule_slots (
    repo_id uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    job text NOT NULL,
    slot timestamptz NOT NULL,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    PRIMARY KEY (repo_id, job, slot)
);
CREATE INDEX schedule_slots_slot ON schedule_slots (slot);

-- +goose Down
DROP TABLE schedule_slots;
DROP INDEX job_runs_rerun_of;
ALTER TABLE job_runs DROP COLUMN rerun_of;
ALTER TABLE job_runs DROP COLUMN attempt;
