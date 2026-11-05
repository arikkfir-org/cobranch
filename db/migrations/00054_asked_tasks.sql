-- Agents asked in chats: a message in any chat, a branch's, an issue's or a private conversation, may ask one of the
-- organization's agents, which gives the agent a task that talks in the message's thread; a task asked in a person's
-- private conversation is private, shown to its assigner alone
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/conversations.md, "Agents in every chat").
-- +goose Up
INSERT INTO task_kinds (kind) VALUES ('chat'), ('issue_chat') ON CONFLICT DO NOTHING;

-- A private task shows its title, instructions, summary, transcript and where it works to its assigner alone, and to
-- others only as a place in its agent's queue. It is a column rather than its thread's conversation, since the task
-- outlives the thread: deleting the conversation's owner deletes the conversation and its threads.
ALTER TABLE agent_tasks ADD COLUMN private boolean NOT NULL DEFAULT false;

-- The task a person's message asked for, which the Console links. Checked at commit, since the message that starts a
-- thread comes before the task that talks in it, in one transaction.
ALTER TABLE chat_messages
    ADD COLUMN asked_task_id uuid REFERENCES agent_tasks (id) ON DELETE SET NULL DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX chat_messages_asked_task ON chat_messages (asked_task_id) WHERE asked_task_id IS NOT NULL;

-- +goose Down
-- Rolling back deletes the tasks asked in chats, the private ones among them; the audit log keeps what they did.
DELETE FROM agent_tasks WHERE kind IN ('chat', 'issue_chat');
DROP INDEX chat_messages_asked_task;
ALTER TABLE chat_messages DROP COLUMN asked_task_id;
ALTER TABLE agent_tasks DROP COLUMN private;
DELETE FROM task_kinds WHERE kind IN ('chat', 'issue_chat');
