-- Branch status: the merge requirements the default branch's /cobranch.yaml names, and what each branch gains as a
-- request to merge into its base
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/branch-status.md, "Merge requirements" and
-- "Branch status"). A push writes the requirements and the branches' graph facts in its transaction; the push worker,
-- and an annotation's own transaction, work out the status from them. The requirements are a parsed copy of a file
-- whose history is Git's, and a branch's status follows from its pushes and annotations, so neither carries trigger
-- audit().
-- +goose Up

-- What a merge into a branch needs, by the branch's name, which it may name before the branch exists: the annotations
-- its incoming head must carry, Successful, and whether history stays linear. A push that moves the default branch
-- replaces its repository's rows with those of /cobranch.yaml at the branch's new tip.
CREATE TABLE merge_requirements (
    repo_id uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    branch text NOT NULL,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    annotations text[] NOT NULL,
    linear_history boolean NOT NULL,
    PRIMARY KEY (repo_id, branch)
);

-- A branch other than the default, as a request to merge into its base: how many commits it has that the base lacks
-- (ahead), and the base has that it lacks (behind); how many of its own are merges, counted only while the base
-- requires linear history; its status, with the checks behind it; and the push it waits for, while the push worker
-- hasn't processed the push that last changed it. All are null on the default branch, and on a branch no push since
-- this migration has worked out, which shows Processing.
ALTER TABLE branches
    ADD COLUMN base text,
    ADD COLUMN ahead integer,
    ADD COLUMN behind integer,
    ADD COLUMN merges integer,
    ADD COLUMN status text CHECK (status IN ('processing', 'blocked', 'empty', 'merged', 'ready')),
    ADD COLUMN checks jsonb,
    ADD COLUMN waits_for uuid REFERENCES pushes (id) ON DELETE SET NULL;
CREATE INDEX branches_waiting ON branches (waits_for) WHERE waits_for IS NOT NULL;
CREATE INDEX branches_head ON branches (repo_id, head) WHERE deleted_at IS NULL;

-- +goose Down
-- Rolling back forgets every branch's status and the requirements; /cobranch.yaml keeps what they were.
DROP INDEX branches_head;
DROP INDEX branches_waiting;
ALTER TABLE branches
    DROP COLUMN waits_for,
    DROP COLUMN checks,
    DROP COLUMN status,
    DROP COLUMN merges,
    DROP COLUMN behind,
    DROP COLUMN ahead,
    DROP COLUMN base;
DROP TABLE merge_requirements;
