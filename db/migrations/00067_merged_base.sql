-- A merged branch's change: the default branch's tip the merge was made onto, so a branch's Files changed still shows
-- what it merged once its base holds it
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/branch-status.md, "Branch status").
-- +goose Up

-- The default branch's tip before the push that made the last merge recorded; NULL while none was, and for a merge
-- whose push is gone.
ALTER TABLE branches ADD COLUMN merged_base text;

-- Merges recorded before now take it from the push that made them: the one that brought the merged head into the
-- default branch, recorded when the merge was.
UPDATE branches b SET merged_base = u ->> 'old'
FROM pushes p, jsonb_array_elements(p.merged) h, jsonb_array_elements(p.ref_updates) u
WHERE b.merged_head IS NOT NULL AND p.repo_id = b.repo_id AND p.committed_at = b.merged_at
    AND h ->> 'sha' = b.merged_head AND (h ->> 'id' = b.id::text OR (h ->> 'id' IS NULL AND h ->> 'branch' = b.name))
    AND u ->> 'name' = 'refs/heads/main' AND u ->> 'old' <> '0000000000000000000000000000000000000000';

-- +goose Down
-- Rolling back forgets the tips; a merged branch's Files changed shows no change again.
ALTER TABLE branches DROP COLUMN merged_base;
