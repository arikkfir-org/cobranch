-- ReviewerOf finds the AI reviewer of a repository's organization: its agent, whether it has a model, and whether it is
-- suspended.
-- name: ReviewerOf :one
SELECT a.principal_id, a.org_id, (a.provider IS NOT NULL)::boolean AS has_model, p.suspended
FROM repositories r JOIN agents a ON a.org_id = r.org_id AND a.purpose = 'review' JOIN principals p ON p.id = a.principal_id
WHERE r.id = $1;

-- ReviewedBranch finds a repository's live branch of a name, and its head.
-- name: ReviewedBranch :one
SELECT id, head FROM branches WHERE repo_id = $1 AND name = $2 AND deleted_at IS NULL;

-- HasReview reports whether a branch has a review task for a head that is still open or ended with its review.
-- name: HasReview :one
SELECT EXISTS (SELECT 1 FROM agent_tasks WHERE branch_id = $1 AND sha = $2 AND kind = 'review'
    AND state IN ('queued', 'running', 'waiting', 'succeeded'))::boolean;

-- CancelStaleReviews cancels a repository's open review tasks whose branch was deleted or moved on from their head,
-- saying which, and answers them.
-- name: CancelStaleReviews :many
UPDATE agent_tasks t SET state = 'cancelled', ended_at = now(),
    reason = CASE WHEN b.deleted_at IS NOT NULL THEN 'The branch was deleted.' ELSE 'A newer push to the branch superseded it.' END
FROM branches b
WHERE b.id = t.branch_id AND t.repo_id = @repo_id AND t.kind = 'review' AND t.state IN ('queued', 'running', 'waiting')
    AND (b.deleted_at IS NOT NULL OR b.head <> t.sha)
RETURNING t.id, t.org_id, t.number, t.agent_id;

-- CancelBranchReviews cancels a branch's open review tasks for whoever asked for another review, and answers them.
-- name: CancelBranchReviews :many
UPDATE agent_tasks SET state = 'cancelled', ended_at = now(), cancelled_by = @cancelled_by, reason = @reason
WHERE branch_id = @branch_id AND kind = 'review' AND state IN ('queued', 'running', 'waiting')
RETURNING id, org_id, number, agent_id;

-- HoldTask has a task that hasn't started wait for a member, for a reason.
-- name: HoldTask :exec
UPDATE agent_tasks SET state = 'waiting', reason = @reason WHERE id = @id AND state = 'queued';
