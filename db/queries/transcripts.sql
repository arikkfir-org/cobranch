-- TaskByID finds a task by its ID, with the names Task gives it, its organization's slug and issue key, its agent's
-- standing instructions and the number of its issue; and of the chat its thread is in, the issue's number for an
-- issue's chat, and the conversation, the slug of the repository it is about and the agent it is with for a private
-- conversation.
-- name: TaskByID :one
SELECT sqlc.embed(t), ag.name AS agent_name, asg.display_name AS assigner_name, cn.display_name AS canceller_name,
    rp.slug AS repo_slug, b.name AS branch_name, o.slug AS org_slug, o.issue_key, ag.instructions AS agent_instructions,
    i.number AS issue_number, ti.number AS thread_issue_number, th.conversation_id, cr.slug AS conversation_repo_slug,
    cv.agent_id AS conversation_agent_id, sp.spent, sp.input_tokens, sp.output_tokens, coalesce(ag.cap, 0)::bigint AS cap
FROM agent_tasks t JOIN agents ag ON ag.principal_id = t.agent_id JOIN organizations o ON o.id = t.org_id
    LEFT JOIN principals asg ON asg.id = t.assigned_by LEFT JOIN principals cn ON cn.id = t.cancelled_by
    LEFT JOIN repositories rp ON rp.id = t.repo_id LEFT JOIN branches b ON b.id = t.branch_id
    LEFT JOIN issues i ON i.id = t.issue_id
    LEFT JOIN chat_threads th ON th.id = t.thread_id LEFT JOIN issues ti ON ti.id = th.issue_id
    LEFT JOIN conversations cv ON cv.id = th.conversation_id LEFT JOIN repositories cr ON cr.id = cv.repo_id
    CROSS JOIN LATERAL (
        SELECT coalesce(sum(s.cost), 0)::bigint AS spent, coalesce(sum(s.input_tokens), 0)::bigint AS input_tokens,
            coalesce(sum(s.output_tokens), 0)::bigint AS output_tokens
        FROM task_spend s WHERE s.task_id = t.id
    ) sp
WHERE t.id = @id;

-- LockTask holds the task's row until the transaction ends, so its transcript grows one entry at a time, and answers
-- what it is of.
-- name: LockTask :one
SELECT org_id, number, state, kind, repo_id, sha FROM agent_tasks WHERE id = $1 FOR UPDATE;

-- name: LastEntry :one
SELECT coalesce(max(seq), 0)::integer AS seq FROM agent_task_entries WHERE task_id = $1;

-- name: TranscriptEntry :one
SELECT * FROM agent_task_entries WHERE task_id = $1 AND seq = $2;

-- name: InsertEntry :one
INSERT INTO agent_task_entries (task_id, seq, kind, body) VALUES (@task_id, @seq, @kind, @body) RETURNING *;

-- Entries lists at most lim of a task's entries numbered above after, in order.
-- name: Entries :many
SELECT * FROM agent_task_entries WHERE task_id = @task_id AND seq > @after ORDER BY seq LIMIT @lim;

-- MoveTask moves the task to state from one of from, with its summary and reason, ending it now when the state ends
-- it: no row when it is in none of from.
-- name: MoveTask :execrows
UPDATE agent_tasks SET state = @state::text, summary = @summary, reason = @reason,
    ended_at = CASE WHEN @state::text IN ('succeeded', 'failed') THEN now() END
WHERE id = @id AND state = ANY(@from_states::text[]);
