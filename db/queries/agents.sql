-- InsertAgentPrincipal makes the principal of kind agent an agent is, owned by its organization.
-- name: InsertAgentPrincipal :exec
INSERT INTO principals (id, kind, display_name, org_id, created_by) VALUES (@id, 'agent', @name, @org_id, sqlc.narg(created_by));

-- InsertAgent records an agent of its organization, perhaps a built-in one made for a purpose.
-- name: InsertAgent :exec
INSERT INTO agents (principal_id, org_id, name, description, instructions, built_in, purpose, created_by)
VALUES (@id, @org_id, @name, @description, @instructions, @built_in, sqlc.narg(purpose), sqlc.narg(created_by));

-- Agents lists an organization's agents by name, its own and its repositories', with the display names of their
-- creators and the slugs of their repositories.
-- name: Agents :many
SELECT sqlc.embed(a), c.display_name AS creator_name, rp.slug AS repo_slug
FROM agents a LEFT JOIN principals c ON c.id = a.created_by LEFT JOIN repositories rp ON rp.id = a.repo_id
WHERE a.org_id = @org_id ORDER BY lower(a.name), a.repo_id NULLS FIRST, a.principal_id;

-- Agent finds one of an organization's agents, with the display name of its creator and the slug of its repository.
-- name: Agent :one
SELECT sqlc.embed(a), c.display_name AS creator_name, rp.slug AS repo_slug
FROM agents a LEFT JOIN principals c ON c.id = a.created_by LEFT JOIN repositories rp ON rp.id = a.repo_id
WHERE a.principal_id = @id AND a.org_id = @org_id;

-- AgentGrants lists the roles the agents ids name hold at their organization and on its repositories, those at the
-- organization first, with the roles' names and the repositories' slugs.
-- name: AgentGrants :many
SELECT g.principal_id, g.role_id, r.name AS role_name, g.scope_id, rp.slug AS repo_slug
FROM grants g JOIN roles r ON r.id = g.role_id LEFT JOIN repositories rp ON g.scope = 'repository' AND rp.id = g.scope_id
WHERE g.principal_id = ANY (@ids::uuid[]) AND g.scope IN ('organization', 'repository')
ORDER BY g.principal_id, g.scope = 'repository', rp.slug, r.name;

-- UpdateAgent changes an agent's fields that aren't null, and its cap when set_cap says so, to cap or to none for null,
-- when it is at version, or at any version when version is negative: none otherwise.
-- name: UpdateAgent :one
UPDATE agents SET name = coalesce(sqlc.narg(name), name), description = coalesce(sqlc.narg(description), description),
    instructions = coalesce(sqlc.narg(instructions), instructions),
    cap = CASE WHEN @set_cap::boolean THEN sqlc.narg(cap)::bigint ELSE cap END, updated_at = now()
WHERE principal_id = @id AND org_id = @org_id AND (@version::bigint < 0 OR version = @version::bigint)
RETURNING *;

-- RenameAgentPrincipal gives an agent's principal its new name, which the Console shows wherever it acted.
-- name: RenameAgentPrincipal :exec
UPDATE principals SET display_name = @name WHERE id = @id AND kind = 'agent';

-- LockAgent holds one of an organization's agents until the transaction ends, and answers its version.
-- name: LockAgent :one
SELECT version FROM agents WHERE principal_id = @id AND org_id = @org_id FOR UPDATE;

-- DeleteAgentPrincipal deletes an agent's principal, and with it the agent, its grants and its tasks.
-- name: DeleteAgentPrincipal :execrows
DELETE FROM principals WHERE id = @id AND kind = 'agent' AND org_id = @org_id::uuid;

-- RepositorySlug finds one of an organization's repositories by ID and answers its slug.
-- name: RepositorySlug :one
SELECT slug FROM repositories WHERE id = @id AND org_id = @org_id;

-- TaskBranch finds one of a repository's live branches for a task, by ID, or by name when the ID is null.
-- name: TaskBranch :one
SELECT id, name FROM branches
WHERE repo_id = @repo_id AND deleted_at IS NULL AND (id = sqlc.narg(id)::uuid OR (sqlc.narg(id)::uuid IS NULL AND name = @name));

-- ThreadBranch answers the branch a chat thread belongs to.
-- name: ThreadBranch :one
SELECT branch_id FROM chat_threads WHERE id = $1;

-- NextTaskNumber gives an organization's next task number, under its counter's row lock until the transaction ends.
-- name: NextTaskNumber :one
INSERT INTO task_numbers (org_id, last) VALUES (@org_id, 1)
ON CONFLICT (org_id) DO UPDATE SET last = task_numbers.last + 1
RETURNING last;

-- InsertTask records a task, Queued.
-- name: InsertTask :exec
INSERT INTO agent_tasks (id, org_id, number, agent_id, kind, assigned_by, title, instructions, repo_id, branch_id, thread_id, issue_id, sha,
    private, run_id)
VALUES (@id, @org_id, @number, @agent_id, @kind, sqlc.narg(assigned_by), @title, @instructions, sqlc.narg(repo_id), sqlc.narg(branch_id),
    sqlc.narg(thread_id), sqlc.narg(issue_id), sqlc.narg(sha), @private, sqlc.narg(run_id));

-- ActiveThreadTask finds the number of a task that talks in the thread and has yet to end.
-- name: ActiveThreadTask :one
SELECT number FROM agent_tasks WHERE thread_id = $1 AND state IN ('queued', 'running', 'waiting') ORDER BY number LIMIT 1;

-- Tasks lists at most lim of an organization's tasks, newest first, numbered below before when it isn't null, of the
-- agent, the states and the repository when they're given, naming no repository or one of readable, and of none of
-- the kinds in hidden, with the names of their agents, assigners, cancellers, repositories and branches, their runs'
-- numbers, what each spent and the most it may spend: its agent's cap, or 0 for none.
-- name: Tasks :many
SELECT sqlc.embed(t), ag.name AS agent_name, asg.display_name AS assigner_name, cn.display_name AS canceller_name,
    rp.slug AS repo_slug, b.name AS branch_name, sp.spent, sp.input_tokens, sp.output_tokens,
    coalesce(ag.cap, 0)::bigint AS cap, jr.number AS run_number
FROM agent_tasks t JOIN agents ag ON ag.principal_id = t.agent_id
    LEFT JOIN principals asg ON asg.id = t.assigned_by LEFT JOIN principals cn ON cn.id = t.cancelled_by
    LEFT JOIN repositories rp ON rp.id = t.repo_id LEFT JOIN branches b ON b.id = t.branch_id
    LEFT JOIN job_runs jr ON jr.id = t.run_id
    CROSS JOIN LATERAL (
        SELECT coalesce(sum(s.cost), 0)::bigint AS spent, coalesce(sum(s.input_tokens), 0)::bigint AS input_tokens,
            coalesce(sum(s.output_tokens), 0)::bigint AS output_tokens
        FROM task_spend s WHERE s.task_id = t.id
    ) sp
WHERE t.org_id = @org_id
    AND (sqlc.narg(agent_id)::uuid IS NULL OR t.agent_id = sqlc.narg(agent_id)::uuid)
    AND (cardinality(@states::text[]) = 0 OR t.state = ANY (@states::text[]))
    AND (sqlc.narg(repo_id)::uuid IS NULL OR t.repo_id = sqlc.narg(repo_id)::uuid)
    AND (t.repo_id IS NULL OR t.repo_id = ANY (@readable::uuid[]))
    AND NOT (t.kind = ANY (@hidden::text[]))
    AND (sqlc.narg(before)::bigint IS NULL OR t.number < sqlc.narg(before)::bigint)
ORDER BY t.number DESC LIMIT @lim;

-- Task finds one of an organization's tasks by its number, with the names of its agent, assigner, canceller,
-- repository and branch, its run's number, what it spent and the most it may spend: its agent's cap, or 0 for none.
-- name: Task :one
SELECT sqlc.embed(t), ag.name AS agent_name, asg.display_name AS assigner_name, cn.display_name AS canceller_name,
    rp.slug AS repo_slug, b.name AS branch_name, sp.spent, sp.input_tokens, sp.output_tokens,
    coalesce(ag.cap, 0)::bigint AS cap, jr.number AS run_number
FROM agent_tasks t JOIN agents ag ON ag.principal_id = t.agent_id
    LEFT JOIN principals asg ON asg.id = t.assigned_by LEFT JOIN principals cn ON cn.id = t.cancelled_by
    LEFT JOIN repositories rp ON rp.id = t.repo_id LEFT JOIN branches b ON b.id = t.branch_id
    LEFT JOIN job_runs jr ON jr.id = t.run_id
    CROSS JOIN LATERAL (
        SELECT coalesce(sum(s.cost), 0)::bigint AS spent, coalesce(sum(s.input_tokens), 0)::bigint AS input_tokens,
            coalesce(sum(s.output_tokens), 0)::bigint AS output_tokens
        FROM task_spend s WHERE s.task_id = t.id
    ) sp
WHERE t.org_id = @org_id AND t.number = @number;

-- CancelTask cancels one of an organization's active tasks for by, when it is at version, or at any version when
-- version is negative: none otherwise.
-- name: CancelTask :one
UPDATE agent_tasks SET state = 'cancelled', cancelled_by = @cancelled_by, ended_at = now(), reason = @reason
WHERE org_id = @org_id AND number = @number AND state IN ('queued', 'running', 'waiting')
    AND (@version::bigint < 0 OR version = @version::bigint)
RETURNING *;

-- RepositoryAgents lists the agents a repository's main /cobranch.yaml declares or declared, the retired ones too.
-- name: RepositoryAgents :many
SELECT principal_id, name, retired_at FROM agents WHERE repo_id = @repo_id ORDER BY name;

-- InsertRepositoryAgent records an agent a repository's main /cobranch.yaml declares, on a model or on none.
-- name: InsertRepositoryAgent :exec
INSERT INTO agents (principal_id, org_id, repo_id, name, description, instructions, image, provider, model, permissions, secrets,
    line, created_by)
VALUES (@id, @org_id, @repo_id, @name, @description, @instructions, @image, sqlc.narg(provider), sqlc.narg(model),
    @permissions::text[], @secrets, @line, sqlc.narg(created_by));

-- UpdateRepositoryAgent records what a repository's main /cobranch.yaml now says of one of its agents, bringing it back
-- when retired, and answers whether anything changed: a row unchanged keeps its version.
-- name: UpdateRepositoryAgent :execrows
UPDATE agents SET description = @description, instructions = @instructions, image = @image, provider = sqlc.narg(provider),
    model = sqlc.narg(model), permissions = @permissions::text[], secrets = @secrets, line = @line, retired_at = NULL, updated_at = now()
WHERE principal_id = @id AND repo_id = @repo_id
    AND (description, instructions, image, provider, model, permissions, secrets, line, retired_at IS NULL)
        IS DISTINCT FROM (@description, @instructions, @image, sqlc.narg(provider), sqlc.narg(model), @permissions::text[], @secrets, @line, true);

-- RetireRepositoryAgents retires those of a repository's agents its main /cobranch.yaml no longer declares, and
-- answers their IDs. A retired agent runs on no model, so it holds none of the organization's models or providers in
-- use; declaring it again puts it back on the file's.
-- name: RetireRepositoryAgents :many
UPDATE agents SET retired_at = now(), provider = NULL, model = NULL, updated_at = now()
WHERE repo_id = @repo_id AND retired_at IS NULL AND NOT (name = ANY (@declared::text[]))
RETURNING principal_id;

-- CancelAgentTasks cancels the active tasks of the agents ids names, for reason, by nobody, and answers them.
-- name: CancelAgentTasks :many
UPDATE agent_tasks SET state = 'cancelled', ended_at = now(), reason = @reason
WHERE agent_id = ANY (@ids::uuid[]) AND state IN ('queued', 'running', 'waiting')
RETURNING id, number;

-- HasModel reports whether the organization has the model of the provider.
-- name: HasModel :one
SELECT EXISTS (SELECT 1 FROM models WHERE org_id = @org_id AND provider = @provider AND model = @model);

-- DefaultModel finds the organization's default model.
-- name: DefaultModel :one
SELECT provider, model FROM models WHERE org_id = @org_id AND is_default;
