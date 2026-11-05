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

-- IssueByNumber finds one of an organization's issues, with its organization's slug and issue key, the names of who it
-- is assigned to, who created it and the repository it is about, and its labels by name, its assignee's newest task on
-- it when its assignee is an agent, and the live branch linked to it last.
-- name: IssueByNumber :one
SELECT sqlc.embed(i), o.slug AS org_slug, o.issue_key, a.display_name AS assignee_name, a.kind AS assignee_kind,
       c.display_name AS creator_name, r.slug AS repo_slug, r.name AS repo_name,
       (SELECT coalesce(jsonb_agg(jsonb_build_object('id', l.id, 'name', l.name, 'color', l.color) ORDER BY lower(l.name), l.id), '[]')
        FROM issue_labels il JOIN labels l ON l.id = il.label_id WHERE il.issue_id = i.id)::jsonb AS labels,
       (SELECT jsonb_build_object('number', t.number, 'state', t.state, 'repo', t.repo_id) FROM agent_tasks t
        WHERE t.issue_id = i.id AND t.agent_id = i.assignee_id ORDER BY t.created_at DESC, t.id DESC LIMIT 1)::jsonb AS task,
       (SELECT jsonb_build_object('repo', b.repo_id, 'slug', br.slug, 'name', b.name) FROM branch_issues bl
        JOIN branches b ON b.id = bl.branch_id JOIN repositories br ON br.id = b.repo_id
        WHERE bl.issue_id = i.id AND b.deleted_at IS NULL ORDER BY bl.linked_at DESC, b.name LIMIT 1)::jsonb AS branch
FROM issues i
    JOIN organizations o ON o.id = i.org_id
    LEFT JOIN principals a ON a.id = i.assignee_id
    LEFT JOIN principals c ON c.id = i.created_by
    LEFT JOIN repositories r ON r.id = i.repo_id
WHERE i.org_id = $1 AND i.number = $2;

-- ListIssues lists a page of an organization's issues, newest first, as IssueByNumber answers each: those in the
-- states, assigned to the assignee (nobody for the nil UUID, anyone for none), about the repository (none for the nil
-- UUID, any for none), carrying every one of the labels, whose title or description matches the pattern or whose
-- number the text names (any for an empty pattern), and numbered below below (any for 0).
-- name: ListIssues :many
SELECT sqlc.embed(i), o.slug AS org_slug, o.issue_key, a.display_name AS assignee_name, a.kind AS assignee_kind,
       c.display_name AS creator_name, r.slug AS repo_slug, r.name AS repo_name,
       (SELECT coalesce(jsonb_agg(jsonb_build_object('id', l.id, 'name', l.name, 'color', l.color) ORDER BY lower(l.name), l.id), '[]')
        FROM issue_labels il JOIN labels l ON l.id = il.label_id WHERE il.issue_id = i.id)::jsonb AS labels,
       (SELECT jsonb_build_object('number', t.number, 'state', t.state, 'repo', t.repo_id) FROM agent_tasks t
        WHERE t.issue_id = i.id AND t.agent_id = i.assignee_id ORDER BY t.created_at DESC, t.id DESC LIMIT 1)::jsonb AS task,
       (SELECT jsonb_build_object('repo', b.repo_id, 'slug', br.slug, 'name', b.name) FROM branch_issues bl
        JOIN branches b ON b.id = bl.branch_id JOIN repositories br ON br.id = b.repo_id
        WHERE bl.issue_id = i.id AND b.deleted_at IS NULL ORDER BY bl.linked_at DESC, b.name LIMIT 1)::jsonb AS branch
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
  AND (SELECT count(*) FROM issue_labels il WHERE il.issue_id = i.id AND il.label_id = ANY (@label_ids::uuid[])) = cardinality(@label_ids::uuid[])
  AND (@pattern::text = '' OR i.title ILIKE @pattern OR i.description ILIKE @pattern OR i.number = @number::integer)
  AND (@below::integer = 0 OR i.number < @below::integer)
ORDER BY i.number DESC
LIMIT @lim::integer;

-- IssueCounts counts an organization's issues that ListIssues' filters but its states and page match: those open and
-- those closed.
-- name: IssueCounts :one
SELECT count(*) FILTER (WHERE i.state = ANY (@open::text[]))::integer AS open,
       count(*) FILTER (WHERE NOT i.state = ANY (@open::text[]))::integer AS closed
FROM issues i
WHERE i.org_id = @org_id
  AND (sqlc.narg(assignee_id)::uuid IS NULL
       OR i.assignee_id IS NOT DISTINCT FROM nullif(sqlc.narg(assignee_id)::uuid, '00000000-0000-0000-0000-000000000000'))
  AND (sqlc.narg(repo_id)::uuid IS NULL
       OR i.repo_id IS NOT DISTINCT FROM nullif(sqlc.narg(repo_id)::uuid, '00000000-0000-0000-0000-000000000000'))
  AND (SELECT count(*) FROM issue_labels il WHERE il.issue_id = i.id AND il.label_id = ANY (@label_ids::uuid[])) = cardinality(@label_ids::uuid[])
  AND (@pattern::text = '' OR i.title ILIKE @pattern OR i.description ILIKE @pattern OR i.number = @number::integer);

-- UpdateIssue writes an issue's fields over the version it was read at: no row when it is at another now.
-- name: UpdateIssue :one
UPDATE issues SET title = @title, description = @description, state = @state, priority = @priority,
    assignee_id = sqlc.narg(assignee_id), repo_id = sqlc.narg(repo_id), closed_at = sqlc.narg(closed_at), updated_at = now()
WHERE id = @id AND version = @version
RETURNING version, updated_at;

-- DeleteIssue deletes an issue at the version it was read at: no row when it is at another now.
-- name: DeleteIssue :execrows
DELETE FROM issues WHERE id = @id AND version = @version;

-- DeleteIssueEvents deletes an issue's activity and its chat's events, which go with it.
-- name: DeleteIssueEvents :exec
DELETE FROM events WHERE subject_id = @issue_id AND (kind LIKE 'issue.%' OR kind LIKE 'chat.%');

-- IssueEvents lists an issue's activity, oldest first, with the display names of those who acted.
-- name: IssueEvents :many
SELECT e.id, e.kind, e.at, e.details, e.actor_id, p.display_name AS actor_name
FROM events e LEFT JOIN principals p ON p.id = e.actor_id
WHERE e.subject_id = @issue_id AND e.kind LIKE 'issue.%'
ORDER BY e.seq;

-- IssueTaskStarts lists the tasks an issue's assignments gave agents that have started, oldest first: each with its
-- number, its agent and the agent's display name, when it started, the repository it works in and its branch's name.
-- name: IssueTaskStarts :many
SELECT t.id, t.number, t.started_at::timestamptz AS started_at, t.agent_id, p.display_name AS agent_name, t.repo_id, b.name AS branch_name
FROM agent_tasks t
JOIN principals p ON p.id = t.agent_id
LEFT JOIN branches b ON b.id = t.branch_id
WHERE t.issue_id = @issue_id AND t.kind = 'issue' AND t.started_at IS NOT NULL
ORDER BY t.started_at, t.id;

-- IssueAssignee finds whom an organization's issue may be assigned to, by their principal, with their display name and
-- kind: one of its members, or one of its agents.
-- name: IssueAssignee :one
SELECT p.id, p.display_name, p.kind FROM principals p
WHERE p.id = @principal_id
  AND (EXISTS (SELECT 1 FROM memberships m WHERE m.org_id = @org_id AND m.user_id = p.id)
       OR EXISTS (SELECT 1 FROM agents ag WHERE ag.org_id = @org_id AND ag.principal_id = p.id));

-- OrganizationRepository finds one of an organization's repositories.
-- name: OrganizationRepository :one
SELECT id, slug, name FROM repositories WHERE org_id = $1 AND id = $2;
