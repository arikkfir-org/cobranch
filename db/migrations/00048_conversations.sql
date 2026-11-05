-- Conversations: beside the branches' and the issues' chats, which their subjects' readers share, each person has a
-- conversation of their own about their organization, about each of its repositories and with each of its agents,
-- which only they read (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/conversations.md).
-- Its threads are chat threads, as a branch's are; each change to it appends an event whose subject is the
-- conversation, which orders its live updates.
-- +goose Up

-- A person's private conversation about an organization, one of its repositories or one of its agents: one per person
-- per subject, made by the change that starts its first thread. Its ID comes from the person and the subject, so that
-- its events and those who follow it name it before it is made.
CREATE TABLE conversations (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    owner_id uuid NOT NULL REFERENCES principals (id) ON DELETE CASCADE,
    repo_id uuid REFERENCES repositories (id) ON DELETE CASCADE,
    agent_id uuid REFERENCES agents (principal_id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT conversations_subject CHECK (num_nonnulls(repo_id, agent_id) <= 1),
    CONSTRAINT conversations_one UNIQUE NULLS NOT DISTINCT (owner_id, org_id, repo_id, agent_id)
);
CREATE INDEX conversations_org ON conversations (org_id);
CREATE INDEX conversations_repo ON conversations (repo_id) WHERE repo_id IS NOT NULL;
CREATE INDEX conversations_agent ON conversations (agent_id) WHERE agent_id IS NOT NULL;

-- A thread is of a branch's chat, of an issue's or of a conversation, which points at no file: a repository's
-- conversation is about the repository, not one of its commits.
ALTER TABLE chat_threads
    ADD COLUMN conversation_id uuid REFERENCES conversations (id) ON DELETE CASCADE,
    DROP CONSTRAINT chat_threads_subject,
    ADD CONSTRAINT chat_threads_subject CHECK (num_nonnulls(branch_id, issue_id, conversation_id) = 1),
    ADD CONSTRAINT chat_threads_conversation_unanchored CHECK (conversation_id IS NULL OR sha IS NULL);
CREATE INDEX chat_threads_conversation ON chat_threads (conversation_id, id) WHERE conversation_id IS NOT NULL;

CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON conversations FOR EACH ROW EXECUTE FUNCTION audit();

-- +goose Down
-- Rolling back forgets the conversations and their events; the audit log keeps what they said.
DELETE FROM events WHERE kind LIKE 'chat.%' AND subject_id IN (SELECT id FROM conversations);
DELETE FROM chat_threads WHERE conversation_id IS NOT NULL;
DROP INDEX chat_threads_conversation;
ALTER TABLE chat_threads
    DROP CONSTRAINT chat_threads_conversation_unanchored,
    DROP CONSTRAINT chat_threads_subject,
    DROP COLUMN conversation_id,
    ADD CONSTRAINT chat_threads_subject CHECK (num_nonnulls(branch_id, issue_id) = 1);
DROP TABLE conversations;
