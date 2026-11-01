-- Annotations: principals' verdicts on commits, such as ci/build passing, which a branch's merge requirements name
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/branch-status.md, "Annotations").
-- +goose Up

-- An annotation on a commit: at most one of each name there, set and changed only by its principal (created_by), in
-- any order of its four statuses, and deleted by its principal or by one holding annotations.delete_any, which frees
-- its name. Its run starts when it is set, and again when it goes back from failed or successful to pending or
-- in_progress; completed_at is when it last reached failed or successful, null while it runs.
CREATE TABLE annotations (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    repo_id uuid NOT NULL,
    sha text NOT NULL,
    name text NOT NULL,
    status text NOT NULL CHECK (status IN ('pending', 'in_progress', 'failed', 'successful')),
    summary text NOT NULL DEFAULT '',
    link text NOT NULL DEFAULT '',
    created_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    FOREIGN KEY (repo_id, sha) REFERENCES commits (repo_id, sha) ON DELETE CASCADE,
    UNIQUE (repo_id, sha, name)
);
CREATE INDEX annotations_created_by ON annotations (created_by);

CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON annotations FOR EACH ROW EXECUTE FUNCTION audit();

-- +goose Down
-- Rolling back forgets every annotation; the audit log keeps what they were.
DROP TABLE annotations;
