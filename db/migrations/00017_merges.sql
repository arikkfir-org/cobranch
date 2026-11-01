-- Merges: what a push into the default branch merged, and the merge each branch keeps of it
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/branch-status.md, "Merging"). The push's
-- transaction lists the branches whose heads the push brings into the default branch; the push worker records each
-- branch's merge for the pusher, and the branch is Merged while its head is the one merged.
-- +goose Up

-- The branches whose heads a push brought into the default branch, [{"branch", "sha"}], as its manifest named them.
ALTER TABLE pushes ADD COLUMN merged jsonb NOT NULL DEFAULT '[]';

-- The last merge recorded of a branch into its base: the head merged, who pushed it, and when the push was recorded.
ALTER TABLE branches
    ADD COLUMN merged_head text,
    ADD COLUMN merged_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    ADD COLUMN merged_at timestamptz;

-- +goose Down
-- Rolling back forgets every merge recorded; the branches merged show Empty until merged again.
ALTER TABLE branches
    DROP COLUMN merged_at,
    DROP COLUMN merged_by,
    DROP COLUMN merged_head;
ALTER TABLE pushes DROP COLUMN merged;
