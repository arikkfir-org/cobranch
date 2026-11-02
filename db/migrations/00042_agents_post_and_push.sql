-- Agents posting and pushing: the task a message came from, and each agent's own email, with which its harness commits
-- so that the push worker maps the agent's commits to the agent
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md, "Agents act as principals").
-- +goose Up

-- The task whose agent posted a message, which the Console links; null for a person's message, and once the task is
-- deleted.
ALTER TABLE chat_messages ADD COLUMN task_id uuid REFERENCES agent_tasks (id) ON DELETE SET NULL;
CREATE INDEX chat_messages_task ON chat_messages (task_id) WHERE task_id IS NOT NULL;

-- +goose StatementBegin
-- agent_email gives a new agent its email, <agent ID>@agents.cobranch.invalid, verified: a name under a top-level
-- domain that never resolves, so no mail goes anywhere, and the same whatever the installation's host. Every way an
-- agent is made, the agents store's, a migration's or a seed's, gets it. The address is the agent's whoever held it:
-- only a push's login-less user, which a commit naming the address made, or an identity provider claiming a mailbox
-- that can't exist, could hold it first, and leaving it to them would credit the agent's commits to them.
CREATE FUNCTION agent_email() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO principal_emails (email, principal_id, verified)
    VALUES (NEW.principal_id::text || '@agents.cobranch.invalid', NEW.principal_id, true)
    ON CONFLICT (email) DO UPDATE SET principal_id = EXCLUDED.principal_id, verified = true;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER agent_email AFTER INSERT ON agents FOR EACH ROW EXECUTE FUNCTION agent_email();

INSERT INTO principal_emails (email, principal_id, verified)
SELECT principal_id::text || '@agents.cobranch.invalid', principal_id, true FROM agents
ON CONFLICT (email) DO UPDATE SET principal_id = EXCLUDED.principal_id, verified = true;

-- +goose Down
DROP TRIGGER agent_email ON agents;
DROP FUNCTION agent_email();
DELETE FROM principal_emails e USING agents a
WHERE e.principal_id = a.principal_id AND e.email = a.principal_id::text || '@agents.cobranch.invalid';
DROP INDEX chat_messages_task;
ALTER TABLE chat_messages DROP COLUMN task_id;
