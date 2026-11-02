-- IssueByID finds an issue by its ID, as IssueByNumber answers it.
-- name: IssueByID :one
SELECT sqlc.embed(i), o.slug AS org_slug, o.issue_key, a.display_name AS assignee_name, c.display_name AS creator_name,
       r.slug AS repo_slug, r.name AS repo_name
FROM issues i
    JOIN organizations o ON o.id = i.org_id
    LEFT JOIN principals a ON a.id = i.assignee_id
    LEFT JOIN principals c ON c.id = i.created_by
    LEFT JOIN repositories r ON r.id = i.repo_id
WHERE i.id = $1;

-- IssueBranches lists the branches linked to an issue, the live ones first, by repository and name, with their
-- repositories, their status as requests to merge and how they were linked.
-- name: IssueBranches :many
SELECT b.repo_id, r.slug AS repo_slug, r.name AS repo_name, b.name, b.head, b.status, b.deleted_at, l.linked_by, l.linked_at
FROM branch_issues l
    JOIN branches b ON b.id = l.branch_id
    JOIN repositories r ON r.id = b.repo_id
WHERE l.issue_id = $1
ORDER BY b.deleted_at IS NOT NULL, r.slug, b.name, b.created_at DESC;

-- LinkedLiveBranch finds a live branch of an organization's repository by name, with its repository and its link, if
-- any.
-- name: LinkedLiveBranch :one
SELECT b.id, b.org_id, b.name, r.id AS repo_id, r.slug AS repo_slug, r.name AS repo_name, l.issue_id, l.linked_by
FROM branches b
    JOIN repositories r ON r.id = b.repo_id
    LEFT JOIN branch_issues l ON l.branch_id = b.id
WHERE b.org_id = $1 AND b.repo_id = $2 AND b.name = $3 AND b.deleted_at IS NULL;

-- LockBranchLink locks a live branch, so its link changes one change at a time, and reads the issue it works on, if
-- any: no row once it was deleted. A link's insert waits for the lock, since its key refers to the branch.
-- name: LockBranchLink :one
SELECT l.issue_id FROM branches b LEFT JOIN branch_issues l ON l.branch_id = b.id
WHERE b.id = $1 AND b.deleted_at IS NULL
FOR UPDATE OF b;

-- SetBranchIssue links a branch to an issue, or unlinks it for none, in place of any link it had.
-- name: SetBranchIssue :exec
INSERT INTO branch_issues (branch_id, org_id, issue_id, linked_by) VALUES (@branch_id, @org_id, sqlc.narg(issue_id), @linked_by)
ON CONFLICT (branch_id) DO UPDATE SET issue_id = excluded.issue_id, linked_by = excluded.linked_by, linked_at = now();

-- LinkBranchByName links a branch that has no link to an issue its name names: no row when it has one meanwhile.
-- name: LinkBranchByName :execrows
INSERT INTO branch_issues (branch_id, org_id, issue_id, linked_by) VALUES ($1, $2, $3, 'name') ON CONFLICT (branch_id) DO NOTHING;

-- UnlinkedPushBranches lists the live branches of a push's repository that have no link and that the push created,
-- moved or merged, with their repositories and their organization's issue key.
-- name: UnlinkedPushBranches :many
WITH pushed AS (
    SELECT substr(u ->> 'name', length('refs/heads/') + 1) AS name
    FROM pushes p, jsonb_array_elements(p.ref_updates) u
    WHERE p.id = @push_id AND starts_with(u ->> 'name', 'refs/heads/')
    UNION
    SELECT m ->> 'branch' FROM pushes p, jsonb_array_elements(p.merged) m WHERE p.id = @push_id
)
SELECT b.id, b.org_id, b.name, r.id AS repo_id, r.slug AS repo_slug, r.name AS repo_name, o.issue_key
FROM branches b
    JOIN pushed ON pushed.name = b.name
    JOIN repositories r ON r.id = b.repo_id
    JOIN organizations o ON o.id = b.org_id
WHERE b.repo_id = @repo_id AND b.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM branch_issues l WHERE l.branch_id = b.id)
ORDER BY b.name;

-- UnlinkedBranchesNaming lists an organization's live branches that have no link and whose name holds the text, in
-- any case, with their repositories, oldest first.
-- name: UnlinkedBranchesNaming :many
SELECT b.id, b.org_id, b.name, r.id AS repo_id, r.slug AS repo_slug, r.name AS repo_name
FROM branches b JOIN repositories r ON r.id = b.repo_id
WHERE b.org_id = @org_id AND b.deleted_at IS NULL AND strpos(lower(b.name), lower(@text::text)) > 0
  AND NOT EXISTS (SELECT 1 FROM branch_issues l WHERE l.branch_id = b.id)
ORDER BY b.created_at, b.id;

-- MergedIssues lists the issues linked to the live branches of a repository a push merged, by name, with those
-- branches' names: the merges the push worker recorded.
-- name: MergedIssues :many
SELECT l.issue_id::uuid AS issue_id, i.org_id, b.name
FROM branches b
    JOIN branch_issues l ON l.branch_id = b.id
    JOIN issues i ON i.id = l.issue_id
WHERE b.repo_id = @repo_id AND b.name = ANY (@names::text[]) AND b.deleted_at IS NULL
ORDER BY b.name;

-- MoveIssue moves an issue to a state from one of the states it may leave, as its branches do, and stamps when it
-- closed: no row when it is in none of them now.
-- name: MoveIssue :one
WITH old AS (SELECT o.id, o.state FROM issues o WHERE o.id = @id AND o.state = ANY (@from_states::text[]) FOR UPDATE)
UPDATE issues i SET state = @state::text, updated_at = now(),
    closed_at = CASE WHEN @state::text IN ('done', 'canceled') THEN now() END
FROM old WHERE i.id = old.id
RETURNING old.state AS old_state;
