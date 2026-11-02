-- Teardown and resync: a teardown deletes what its environment holds, Deleting until its objects are gone, and the
-- controller queues, on its own schedule, each environment's resync and the teardowns it retries
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md, "Teardown" and "Applying").
-- +goose Up
ALTER TABLE deliveries DROP CONSTRAINT deliveries_state_check;
ALTER TABLE deliveries ADD CONSTRAINT deliveries_state_check
    CHECK (state IN ('waiting', 'queued', 'rendering', 'applying', 'deleting', 'succeeded', 'failed', 'cancelled'));
ALTER TABLE deliveries DROP CONSTRAINT deliveries_trigger_check;
ALTER TABLE deliveries ADD CONSTRAINT deliveries_trigger_check CHECK (trigger IN ('push', 'redeliver', 'resume', 'schedule'));

-- A teardown that deletes is working, as one that applies is.
DROP INDEX deliveries_active;
CREATE INDEX deliveries_active ON deliveries (environment_id) WHERE state IN ('waiting', 'queued', 'rendering', 'applying', 'deleting');
DROP INDEX deliveries_working;
CREATE INDEX deliveries_working ON deliveries (org_id) WHERE state IN ('rendering', 'applying', 'deleting');

-- +goose Down
-- Rolling back fails the teardowns that delete, which leaves their environments live, and forgets the deliveries the
-- controller queued on its schedule.
UPDATE deliveries SET state = 'failed', reason = 'Cobranch was rolled back while it deleted', ended_at = now(), lease_until = NULL
WHERE state = 'deleting';
DELETE FROM deliveries WHERE trigger = 'schedule';
DROP INDEX deliveries_working;
CREATE INDEX deliveries_working ON deliveries (org_id) WHERE state IN ('rendering', 'applying');
DROP INDEX deliveries_active;
CREATE INDEX deliveries_active ON deliveries (environment_id) WHERE state IN ('waiting', 'queued', 'rendering', 'applying');
ALTER TABLE deliveries DROP CONSTRAINT deliveries_trigger_check;
ALTER TABLE deliveries ADD CONSTRAINT deliveries_trigger_check CHECK (trigger IN ('push', 'redeliver', 'resume'));
ALTER TABLE deliveries DROP CONSTRAINT deliveries_state_check;
ALTER TABLE deliveries ADD CONSTRAINT deliveries_state_check
    CHECK (state IN ('waiting', 'queued', 'rendering', 'applying', 'succeeded', 'failed', 'cancelled'));
