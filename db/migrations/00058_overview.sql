-- The Overview: an organization's figures over a span of days, from its tasks, their spend and its job runs
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/quotas.md, "An organization's figures").
-- +goose Up

-- The Overview counts the organization's tasks queued in its span, and the minutes of its job runs that ended in it, by
-- day; task_spend_org already serves its spend.
CREATE INDEX agent_tasks_org_created ON agent_tasks (org_id, created_at);
CREATE INDEX job_runs_ended ON job_runs (org_id, ended_at) WHERE ended_at IS NOT NULL;

-- +goose Down
DROP INDEX job_runs_ended;
DROP INDEX agent_tasks_org_created;
