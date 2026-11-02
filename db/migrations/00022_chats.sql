-- Branch chats: every branch carries a chat of threads of Markdown messages, a thread perhaps anchored to lines of a
-- file at a commit (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md, "Branch
-- chats"). Each change to a chat appends an event whose subject is its branch, which orders the chat's live updates.
-- +goose Up

-- A thread of a branch's chat. A branch deleted and pushed again is another branch, with a chat of its own. An anchor
-- points the thread at lines start_line to end_line of the file at path of commit sha, and stays there when the branch
-- moves on. Its starter, or a principal holding messages.moderate, resolves it and opens it again; each time bumps its
-- version, which is its ETag.
CREATE TABLE chat_threads (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    repo_id uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    branch_id uuid NOT NULL REFERENCES branches (id) ON DELETE CASCADE,
    sha text,
    path text,
    start_line integer,
    end_line integer,
    created_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    resolved_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    version bigint NOT NULL DEFAULT 1,
    FOREIGN KEY (repo_id, sha) REFERENCES commits (repo_id, sha) ON DELETE CASCADE,
    CHECK (num_nulls(sha, path, start_line, end_line) IN (0, 4)),
    CHECK (start_line >= 1 AND end_line >= start_line),
    CHECK (resolved_by IS NULL OR resolved_at IS NOT NULL)
);
CREATE INDEX chat_threads_branch ON chat_threads (branch_id, id);
CREATE INDEX chat_threads_created_by ON chat_threads (created_by);
CREATE INDEX chat_threads_resolved_by ON chat_threads (resolved_by) WHERE resolved_by IS NOT NULL;

-- A message of a thread, in Markdown. Only its author edits it; its author, or a principal holding messages.moderate,
-- deletes it, which empties it and keeps its place in the thread, marked with who deleted it and when. Its ID may come
-- from its poster, so that posting it again finds it rather than posting it twice. Each change bumps its version, which
-- is its ETag.
CREATE TABLE chat_messages (
    id uuid PRIMARY KEY,
    thread_id uuid NOT NULL REFERENCES chat_threads (id) ON DELETE CASCADE,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    author_id uuid REFERENCES principals (id) ON DELETE SET NULL,
    body text NOT NULL CHECK (char_length(body) <= 20000),
    created_at timestamptz NOT NULL DEFAULT now(),
    edited_at timestamptz,
    deleted_at timestamptz,
    deleted_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    version bigint NOT NULL DEFAULT 1,
    CHECK ((deleted_at IS NULL) = (body <> '')),
    CHECK (deleted_by IS NULL OR deleted_at IS NOT NULL)
);
CREATE INDEX chat_messages_thread ON chat_messages (thread_id, created_at, id);
CREATE INDEX chat_messages_author ON chat_messages (author_id);
CREATE INDEX chat_messages_deleted_by ON chat_messages (deleted_by) WHERE deleted_by IS NOT NULL;

-- A chat's live updates: its events after a cursor, in order.
CREATE INDEX events_chat ON events (subject_id, seq) WHERE kind LIKE 'chat.%';

CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON chat_threads FOR EACH ROW EXECUTE FUNCTION audit();
CREATE TRIGGER bump_version BEFORE UPDATE ON chat_threads FOR EACH ROW EXECUTE FUNCTION bump_version();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON chat_messages FOR EACH ROW EXECUTE FUNCTION audit();
CREATE TRIGGER bump_version BEFORE UPDATE ON chat_messages FOR EACH ROW EXECUTE FUNCTION bump_version();

-- +goose Down
-- Rolling back forgets every chat; the audit log keeps what they said.
DROP INDEX events_chat;
DROP TABLE chat_messages;
DROP TABLE chat_threads;
