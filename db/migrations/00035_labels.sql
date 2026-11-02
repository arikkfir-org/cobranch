-- Labels: an organization's labels, which its issues carry
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/issues.md, "Labels").
-- +goose Up

-- A label of an organization's: its name, unique in it whatever its case; one of five colours, which the Console draws
-- in the design system's tones of both themes; what it is for; and its version, its ETag.
CREATE TABLE labels (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name text NOT NULL,
    color text NOT NULL DEFAULT 'gray' CHECK (color IN ('gray', 'blue', 'green', 'yellow', 'red')),
    description text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX labels_name ON labels (org_id, lower(name));

CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON labels FOR EACH ROW EXECUTE FUNCTION audit();
CREATE TRIGGER bump_version BEFORE UPDATE ON labels FOR EACH ROW EXECUTE FUNCTION bump_version();

-- The labels an issue carries. Deleting the issue or the label deletes the row.
CREATE TABLE issue_labels (
    issue_id uuid NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    label_id uuid NOT NULL REFERENCES labels (id) ON DELETE CASCADE,
    PRIMARY KEY (issue_id, label_id)
);
CREATE INDEX issue_labels_label ON issue_labels (label_id);

CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON issue_labels FOR EACH ROW EXECUTE FUNCTION audit();

-- +goose Down
-- Rolling back forgets every label and which issues carried it; the audit log and the issues' activity keep them.
DROP TABLE issue_labels;
DROP TABLE labels;
