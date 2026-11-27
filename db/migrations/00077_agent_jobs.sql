-- Jobs that name an agent: each run of such a job is one task of the agent, of kind job, at the run's commit, which the
-- run follows to its end (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/repo-agents.md,
-- "Jobs that name an agent").
-- +goose Up
INSERT INTO task_kinds (kind) VALUES ('job') ON CONFLICT DO NOTHING;

-- A job's task names its run, which has one task at most, and works at the run's commit. A run that goes, with its
-- repository, leaves its task's history.
ALTER TABLE agent_tasks ADD COLUMN run_id uuid REFERENCES job_runs (id) ON DELETE SET NULL;
CREATE UNIQUE INDEX agent_tasks_run ON agent_tasks (run_id) WHERE run_id IS NOT NULL;
ALTER TABLE agent_tasks ADD CONSTRAINT agent_tasks_job_run CHECK (kind <> 'job' OR sha IS NOT NULL);

-- +goose Down
-- Rolling back deletes the job tasks; the audit log keeps what they did.
DELETE FROM agent_tasks WHERE kind = 'job';
ALTER TABLE agent_tasks DROP CONSTRAINT agent_tasks_job_run;
ALTER TABLE agent_tasks DROP COLUMN run_id;
DELETE FROM task_kinds WHERE kind = 'job';
