-- A target's namespaceLabels: the labels Cobranch writes on a namespace it makes for one of the target's environments,
-- beside its own, so the namespace meets its cluster's conventions (its pod security level, the labels its gateway
-- admits routes by). Each environment keeps its target's labels as of the last push that queued it a delivery, read
-- from the default branch's /cobranch.yaml as its namespace is
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md, "/cobranch.yaml").

-- +goose Up

ALTER TABLE environments ADD COLUMN namespace_labels jsonb NOT NULL DEFAULT '{}';

-- +goose Down

ALTER TABLE environments DROP COLUMN namespace_labels;
