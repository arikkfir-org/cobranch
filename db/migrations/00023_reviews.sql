-- Reviews: people's and agents' verdicts on commits, which a branch's merge requirements can require
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/reviews.md, "Reviews").
-- +goose Up

-- A review a principal submitted on a commit: approved, changes requested or neutral, with what they wrote. It belongs
-- to the commit, so every branch at that commit shows it. A review is never changed: its reviewer submits another,
-- and their latest on the commit is the one that counts.
CREATE TABLE reviews (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    repo_id uuid NOT NULL,
    sha text NOT NULL,
    reviewer_id uuid REFERENCES principals (id) ON DELETE SET NULL,
    state text NOT NULL CHECK (state IN ('approved', 'changes_requested', 'neutral')),
    body text NOT NULL DEFAULT '',
    submitted_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (repo_id, sha) REFERENCES commits (repo_id, sha) ON DELETE CASCADE
);
CREATE INDEX reviews_commit ON reviews (repo_id, sha, submitted_at);
CREATE INDEX reviews_reviewer ON reviews (reviewer_id);

CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON reviews FOR EACH ROW EXECUTE FUNCTION audit();

-- How many people's approvals of its incoming head a merge into the branch needs, when /cobranch.yaml requires
-- reviews of it; null when it doesn't.
ALTER TABLE merge_requirements ADD COLUMN approvals integer CHECK (approvals BETWEEN 0 AND 10);

-- +goose Down
-- Rolling back forgets every review and the requirement; the audit log and /cobranch.yaml keep what they were.
ALTER TABLE merge_requirements DROP COLUMN approvals;
DROP TABLE reviews;
