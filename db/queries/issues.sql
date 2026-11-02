-- NextIssueNumber counts on the organization's issues: the number its next one takes, never one it gave before. Its
-- row stays locked until the transaction ends, so issues created at once take numbers one after another.
-- name: NextIssueNumber :one
INSERT INTO issue_numbers (org_id, last) VALUES ($1, 1)
ON CONFLICT (org_id) DO UPDATE SET last = issue_numbers.last + 1
RETURNING last;

-- name: InsertIssue :exec
INSERT INTO issues (id, org_id, number, title, description, state, priority, assignee_id, repo_id, created_by, created_at, updated_at, closed_at)
VALUES (@id, @org_id, @number, @title, @description, @state, @priority, sqlc.narg(assignee_id), sqlc.narg(repo_id), sqlc.narg(created_by),
        @created_at, @created_at, sqlc.narg(closed_at));

-- IssueByNumber finds one of an organization's issues, with its organization's slug and issue key, and the names of
-- who it is assigned to, who created it and the repository it is about.
-- name: IssueByNumber :one
SELECT sqlc.embed(i), o.slug AS org_slug, o.issue_key, a.display_name AS assignee_name, c.display_name AS creator_name,
       r.slug AS repo_slug, r.name AS repo_name
FROM issues i
    JOIN organizations o ON o.id = i.org_id
    LEFT JOIN principals a ON a.id = i.assignee_id
    LEFT JOIN principals c ON c.id = i.created_by
    LEFT JOIN repositories r ON r.id = i.repo_id
WHERE i.org_id = $1 AND i.number = $2;

-- ListIssues lists a page of an organization's issues, newest first, as IssueByNumber answers each: those in the
-- states, assigned to the assignee (nobody for the nil UUID, anyone for none), about the repository (none for the nil
-- UUID, any for none), whose title or description matches the pattern or whose number the text names (any for an
-- empty pattern), and numbered below below (any for 0).
-- name: ListIssues :many
SELECT sqlc.embed(i), o.slug AS org_slug, o.issue_key, a.display_name AS assignee_name, c.display_name AS creator_name,
       r.slug AS repo_slug, r.name AS repo_name
FROM issues i
    JOIN organizations o ON o.id = i.org_id
    LEFT JOIN principals a ON a.id = i.assignee_id
    LEFT JOIN principals c ON c.id = i.created_by
    LEFT JOIN repositories r ON r.id = i.repo_id
WHERE i.org_id = @org_id
  AND i.state = ANY (@states::text[])
  AND (sqlc.narg(assignee_id)::uuid IS NULL
       OR i.assignee_id IS NOT DISTINCT FROM nullif(sqlc.narg(assignee_id)::uuid, '00000000-0000-0000-0000-000000000000'))
  AND (sqlc.narg(repo_id)::uuid IS NULL
       OR i.repo_id IS NOT DISTINCT FROM nullif(sqlc.narg(repo_id)::uuid, '00000000-0000-0000-0000-000000000000'))
  AND (@pattern::text = '' OR i.title ILIKE @pattern OR i.description ILIKE @pattern OR i.number = @number::integer)
  AND (@below::integer = 0 OR i.number < @below::integer)
ORDER BY i.number DESC
LIMIT @lim::integer;

-- UpdateIssue writes an issue's fields over the version it was read at: no row when it is at another now.
-- name: UpdateIssue :one
UPDATE issues SET title = @title, description = @description, state = @state, priority = @priority,
    assignee_id = sqlc.narg(assignee_id), repo_id = sqlc.narg(repo_id), closed_at = sqlc.narg(closed_at), updated_at = now()
WHERE id = @id AND version = @version
RETURNING version, updated_at;

-- DeleteIssue deletes an issue at the version it was read at: no row when it is at another now.
-- name: DeleteIssue :execrows
DELETE FROM issues WHERE id = @id AND version = @version;

-- DeleteIssueEvents deletes an issue's activity, which goes with it.
-- name: DeleteIssueEvents :exec
DELETE FROM events WHERE subject_id = @issue_id AND kind LIKE 'issue.%';

-- IssueEvents lists an issue's activity, oldest first, with the display names of those who acted.
-- name: IssueEvents :many
SELECT e.id, e.kind, e.at, e.details, e.actor_id, p.display_name AS actor_name
FROM events e LEFT JOIN principals p ON p.id = e.actor_id
WHERE e.subject_id = @issue_id AND e.kind LIKE 'issue.%'
ORDER BY e.seq;

-- OrganizationMember finds one of an organization's members, by their principal, with their display name.
-- name: OrganizationMember :one
SELECT p.id, p.display_name FROM memberships m JOIN principals p ON p.id = m.user_id WHERE m.org_id = $1 AND m.user_id = $2;

-- OrganizationRepository finds one of an organization's repositories.
-- name: OrganizationRepository :one
SELECT id, slug, name FROM repositories WHERE org_id = $1 AND id = $2;
