-- LockSpendingTask holds a task until the transaction ends, and answers what Cobranch prices its calls and caps it with:
-- its organization, agent and state, its agent's model and cap, and that model's prices and cap.
-- name: LockSpendingTask :one
SELECT t.org_id, t.number, t.agent_id, t.state, t.kind, t.repo_id, t.sha, ag.provider, ag.model, ag.cap AS agent_cap,
    m.input_price, m.output_price, m.cap AS model_cap
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
