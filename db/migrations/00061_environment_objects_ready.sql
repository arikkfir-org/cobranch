-- What the Console shows of each object an environment holds besides its health: how many of its replicas, pods or
-- completions are ready, as the object's status counts them (2/2), and when the cluster created it, for its age
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md, "Health and revision"). The
-- controller sets both at each health read, as it sets the health.
-- +goose Up
ALTER TABLE environment_objects ADD COLUMN ready text NOT NULL DEFAULT '';
ALTER TABLE environment_objects ADD COLUMN created_at timestamptz;

-- +goose Down
ALTER TABLE environment_objects DROP COLUMN created_at;
ALTER TABLE environment_objects DROP COLUMN ready;
