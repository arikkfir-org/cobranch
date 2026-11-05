-- LockSpendingTask holds a task until the transaction ends, and answers what Cobranch prices its calls and caps it with:
-- its organization, agent and state, its agent's model and cap, and that model's prices.
-- name: LockSpendingTask :one
SELECT t.org_id, t.number, t.agent_id, t.state, t.kind, t.repo_id, t.sha, ag.provider, ag.model, ag.cap,
    m.input_price, m.output_price
FROM agent_tasks t JOIN agents ag ON ag.principal_id = t.agent_id
    LEFT JOIN models m ON m.org_id = ag.org_id AND m.provider = ag.provider AND m.model = ag.model
WHERE t.id = @id
FOR UPDATE OF t;

-- ModelPrices answers what one of an organization's models costs per million tokens it reads and writes.
-- name: ModelPrices :one
SELECT input_price, output_price FROM models WHERE org_id = @org_id AND provider = @provider AND model = @model;

-- InsertSpend records a call of a task's model, unless the task has it under that ID.
-- name: InsertSpend :execrows
INSERT INTO task_spend (task_id, id, org_id, agent_id, provider, model, input_tokens, output_tokens, cost)
VALUES (@task_id, @id, @org_id, @agent_id, @provider, @model, @input_tokens, @output_tokens, @cost)
ON CONFLICT (task_id, id) DO NOTHING;

-- TaskSpent answers what a task spent, and the tokens its model read and wrote.
-- name: TaskSpent :one
SELECT coalesce(sum(cost), 0)::bigint AS spent, coalesce(sum(input_tokens), 0)::bigint AS input_tokens,
    coalesce(sum(output_tokens), 0)::bigint AS output_tokens
FROM task_spend WHERE task_id = @task_id;

-- MarkCapReached records that a task failed on reaching its agent's cap.
-- name: MarkCapReached :exec
UPDATE agent_tasks SET cap_reached = true WHERE id = @id;

-- AgentSpend answers each of an organization's agents, by name, with its model and cap and what it spent in
-- [since, until): its tasks queued then, of every repository and kind, counted by how they stand, what its calls then
-- cost, and the costliest of those tasks that names no repository or one of readable, is of none of the kinds in
-- hidden, and isn't private to another than private_for, with what it spent in all, number 0 for none. A task failed on its cap when it reached its agent's cap then,
-- whatever the cap is now.
-- name: AgentSpend :many
WITH queued AS (
    SELECT t.agent_id, t.number, t.title, t.state, t.cap_reached,
        (SELECT coalesce(sum(s.cost), 0) FROM task_spend s WHERE s.task_id = t.id)::bigint AS spent,
        ((t.repo_id IS NULL OR t.repo_id = ANY (@readable::uuid[])) AND NOT (t.kind = ANY (@hidden::text[]))
            AND (NOT t.private OR coalesce(t.assigned_by = @private_for::uuid, false)))::boolean AS shown
    FROM agent_tasks t
    WHERE t.org_id = @org_id AND t.created_at >= @since AND t.created_at < @until
), calls AS (
    SELECT s.agent_id, sum(s.cost)::bigint AS spent
    FROM task_spend s
    WHERE s.org_id = @org_id AND s.created_at >= @since AND s.created_at < @until
    GROUP BY s.agent_id
), costliest AS (
    SELECT DISTINCT ON (q.agent_id) q.agent_id, q.number, q.title, q.spent
    FROM queued q WHERE q.shown
    ORDER BY q.agent_id, q.spent DESC, q.number DESC
)
SELECT ag.principal_id, ag.name, ag.provider, ag.model, coalesce(ag.cap, 0)::bigint AS cap,
    count(q.number)::bigint AS tasks,
    count(q.number) FILTER (WHERE q.cap_reached)::bigint AS failed_on_cap,
    count(q.number) FILTER (WHERE q.state IN ('queued', 'running', 'waiting'))::bigint AS active,
    coalesce(max(c.spent), 0)::bigint AS spent,
    coalesce(max(cl.number), 0)::bigint AS costliest_number, coalesce(max(cl.title), '')::text AS costliest_title,
    coalesce(max(cl.spent), 0)::bigint AS costliest_spent
FROM agents ag
    LEFT JOIN queued q ON q.agent_id = ag.principal_id
    LEFT JOIN calls c ON c.agent_id = ag.principal_id
    LEFT JOIN costliest cl ON cl.agent_id = ag.principal_id
WHERE ag.org_id = @org_id
GROUP BY ag.principal_id
ORDER BY lower(ag.name), ag.principal_id;

-- ModelSpend answers each of an organization's models, and each other model its tasks' calls asked in [since, until),
-- by provider and name, with what those calls read, wrote and cost and how many tasks made them.
-- name: ModelSpend :many
SELECT coalesce(m.provider, c.provider)::text AS provider, coalesce(m.model, c.model)::text AS model,
    coalesce(c.tasks, 0)::bigint AS tasks, coalesce(c.input_tokens, 0)::bigint AS input_tokens,
    coalesce(c.output_tokens, 0)::bigint AS output_tokens, coalesce(c.spent, 0)::bigint AS spent
FROM (SELECT md.provider, md.model FROM models md WHERE md.org_id = @org_id) m
    FULL JOIN (
        SELECT s.provider, s.model, count(DISTINCT s.task_id)::bigint AS tasks,
            sum(s.input_tokens)::bigint AS input_tokens, sum(s.output_tokens)::bigint AS output_tokens,
            sum(s.cost)::bigint AS spent
        FROM task_spend s
        WHERE s.org_id = @org_id AND s.created_at >= @since AND s.created_at < @until
        GROUP BY s.provider, s.model
    ) c ON c.provider = m.provider AND c.model = m.model
ORDER BY 1, 2;
