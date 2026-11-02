-- LockLiveBranches locks a repository's live branches, by ID, as every transaction that changes their status locks
-- them, so that those transactions never wait for each other in a circle.
-- name: LockLiveBranches :exec
SELECT id FROM branches WHERE repo_id = $1 AND deleted_at IS NULL ORDER BY id FOR UPDATE;

-- name: DeleteMergeRequirements :exec
DELETE FROM merge_requirements WHERE repo_id = $1;

-- name: InsertMergeRequirements :batchexec
INSERT INTO merge_requirements (repo_id, branch, org_id, annotations, linear_history)
VALUES (@repo_id, @branch, @org_id, @annotations, @linear_history);

-- MergeRequirementsOf finds what a merge into a repository's branch needs: none when /cobranch.yaml names no such
-- branch.
-- name: MergeRequirementsOf :one
SELECT annotations, linear_history FROM merge_requirements WHERE repo_id = $1 AND branch = $2;

-- SetBranchFacts stores what a push's objects say of a live branch against its base, and has it wait for the push,
-- when the push moved it or its base (force), or the facts changed, or none were stored before.
-- name: SetBranchFacts :batchexec
UPDATE branches SET base = @base::text, ahead = @ahead::integer, behind = @behind::integer, merges = sqlc.narg(merges)::integer,
    status = 'processing', waits_for = @push_id::uuid
WHERE repo_id = @repo_id AND name = @name AND deleted_at IS NULL
  AND (@force::boolean OR status IS NULL OR ahead IS DISTINCT FROM @ahead::integer OR behind IS DISTINCT FROM @behind::integer
       OR merges IS DISTINCT FROM sqlc.narg(merges)::integer);

-- BranchesWaitingFor lists the live branches that wait for a push, by ID, locked until the transaction ends.
-- name: BranchesWaitingFor :many
SELECT id, repo_id, head, base, ahead, behind, merges, waits_for, merged_head FROM branches
WHERE waits_for = $1 AND deleted_at IS NULL
ORDER BY id
FOR UPDATE;

-- BranchesAt lists a repository's live branches whose head is a commit and that have a base, by ID, locked until the
-- transaction ends.
-- name: BranchesAt :many
SELECT id, repo_id, head, base, ahead, behind, merges, waits_for, merged_head FROM branches
WHERE repo_id = $1 AND head = $2 AND deleted_at IS NULL AND base IS NOT NULL
ORDER BY id
FOR UPDATE;

-- name: SetBranchStatus :exec
UPDATE branches SET status = @status::text, checks = @checks, waits_for = NULL WHERE id = @id;

-- RecordMerge records a merge of a branch into its base, at the head merged, for the pusher, at when Cobranch
-- recorded the push: the branch the push's transaction found by its ID, deleted since or not, or, for a push recorded
-- before pushes kept their branches' IDs, the live branch of its name.
-- name: RecordMerge :batchexec
UPDATE branches SET merged_head = @head::text, merged_by = @merged_by, merged_at = @merged_at::timestamptz
WHERE repo_id = @repo_id AND (id = sqlc.narg(id)::uuid OR (sqlc.narg(id)::uuid IS NULL AND name = @name AND deleted_at IS NULL));

-- LiveBranchIDs finds the IDs of those of a repository's live branches that names name.
-- name: LiveBranchIDs :many
SELECT id, name FROM branches WHERE repo_id = @repo_id AND name = ANY (@names::text[]) AND deleted_at IS NULL;

-- IncomingBranches finds those of a repository's live branches that names name, as a push into a branch with merge
-- requirements finds them before it applies: their heads, bases, status and checks.
-- name: IncomingBranches :many
SELECT name, head, base, status, checks FROM branches WHERE repo_id = @repo_id AND name = ANY (@names::text[]) AND deleted_at IS NULL;
