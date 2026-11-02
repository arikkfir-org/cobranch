-- ChatSubject finds one of a repository's live branches by name, as the subject of its chat, with the commit it is at
-- and the slugs of the repository and its organization.
-- name: ChatSubject :one
SELECT b.id, b.org_id, b.repo_id, b.name, b.head, r.slug AS repo_slug, o.slug AS org_slug
FROM branches b JOIN repositories r ON r.id = b.repo_id JOIN organizations o ON o.id = b.org_id
WHERE b.repo_id = $1 AND b.name = $2 AND b.deleted_at IS NULL;

-- HasCommit reports whether a repository holds a commit.
-- name: HasCommit :one
SELECT EXISTS (SELECT 1 FROM commits WHERE repo_id = $1 AND sha = $2);

-- LockChat holds the changes to a subject's chat to one transaction at a time, until it ends, so that the chat's events
-- commit in the order of their seq and a reader that saw one has seen every one before it.
-- name: LockChat :exec
SELECT pg_advisory_xact_lock(hashtextextended('cobranch_chats:' || @subject_id::text, 0));

-- NotifyChat wakes the servers that listen, once the change's transaction commits, naming the subject whose chat it
-- changed.
-- name: NotifyChat :exec
SELECT pg_notify('cobranch_chats', @subject_id::text);

-- ListenChats has the connection hear NotifyChat's notifications.
-- name: ListenChats :exec
LISTEN cobranch_chats;

-- ChatCursor is where a subject's chat updates stand: the seq of its chat's last event, 0 before the first.
-- name: ChatCursor :one
SELECT coalesce(max(seq), 0)::bigint FROM events WHERE subject_id = @subject_id AND kind LIKE 'chat.%';

-- ChatEvents lists at most lim of a subject's chat events after seq after, in order.
-- name: ChatEvents :many
SELECT seq::bigint AS seq, kind, details FROM events
WHERE subject_id = @subject_id AND kind LIKE 'chat.%' AND seq > @after::bigint
ORDER BY seq LIMIT @lim;

-- ChatThreads lists at most lim of a branch's chat threads, newest first, from the one before before names, or from
-- the newest when it is null, with the display names of their starters and resolvers.
-- name: ChatThreads :many
SELECT sqlc.embed(t), s.display_name AS starter_name, rb.display_name AS resolver_name
FROM chat_threads t LEFT JOIN principals s ON s.id = t.created_by LEFT JOIN principals rb ON rb.id = t.resolved_by
WHERE t.repo_id = @repo_id AND t.branch_id = @branch_id AND (sqlc.narg(before)::uuid IS NULL OR t.id < sqlc.narg(before)::uuid)
ORDER BY t.id DESC LIMIT @lim;

-- ChatThreadsByID finds those of a branch's chat threads that ids name, with the display names of their starters and
-- resolvers.
-- name: ChatThreadsByID :many
SELECT sqlc.embed(t), s.display_name AS starter_name, rb.display_name AS resolver_name
FROM chat_threads t LEFT JOIN principals s ON s.id = t.created_by LEFT JOIN principals rb ON rb.id = t.resolved_by
WHERE t.repo_id = @repo_id AND t.branch_id = @branch_id AND t.id = ANY (@ids::uuid[]);

-- ChatMessagesOf lists the messages of the threads that thread_ids name, by thread and then in the order they were
-- posted, with the display names of their authors and deleters.
-- name: ChatMessagesOf :many
SELECT sqlc.embed(m), a.display_name AS author_name, d.display_name AS deleter_name
FROM chat_messages m LEFT JOIN principals a ON a.id = m.author_id LEFT JOIN principals d ON d.id = m.deleted_by
WHERE m.thread_id = ANY (@thread_ids::uuid[])
ORDER BY m.thread_id, m.created_at, m.id;

-- ChatMessagesByID finds the messages that ids name, with the display names of their authors and deleters.
-- name: ChatMessagesByID :many
SELECT sqlc.embed(m), a.display_name AS author_name, d.display_name AS deleter_name
FROM chat_messages m LEFT JOIN principals a ON a.id = m.author_id LEFT JOIN principals d ON d.id = m.deleted_by
WHERE m.id = ANY (@ids::uuid[]);

-- InsertChatThread starts a thread: none when one of its ID is there already.
-- name: InsertChatThread :one
INSERT INTO chat_threads (id, org_id, repo_id, branch_id, sha, path, start_line, end_line, created_by)
VALUES (@id, @org_id, @repo_id, @branch_id, sqlc.narg(sha), sqlc.narg(path), sqlc.narg(start_line), sqlc.narg(end_line), @created_by)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- InsertChatMessage posts a message: none when one of its ID is there already.
-- name: InsertChatMessage :one
INSERT INTO chat_messages (id, thread_id, org_id, author_id, body)
VALUES (@id, @thread_id, @org_id, @author_id, @body)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- EditChatMessage changes a live message's body, when it is at version, or at any version when version is negative:
-- none otherwise.
-- name: EditChatMessage :one
UPDATE chat_messages SET body = @body, edited_at = now()
WHERE id = @id AND deleted_at IS NULL AND (@version::bigint < 0 OR version = @version::bigint)
RETURNING *;

-- DeleteChatMessage empties a live message and marks who deleted it, when it is at version, or at any version when
-- version is negative: none otherwise.
-- name: DeleteChatMessage :one
UPDATE chat_messages SET body = '', deleted_at = now(), deleted_by = @deleted_by
WHERE id = @id AND deleted_at IS NULL AND (@version::bigint < 0 OR version = @version::bigint)
RETURNING *;

-- ResolveChatThread resolves a thread for by, or opens it again when by is null, when it is at version, or at any
-- version when version is negative: none otherwise.
-- name: ResolveChatThread :one
UPDATE chat_threads SET resolved_at = CASE WHEN sqlc.narg(by)::uuid IS NULL THEN NULL ELSE now() END, resolved_by = sqlc.narg(by)::uuid
WHERE id = @id AND (@version::bigint < 0 OR version = @version::bigint)
RETURNING *;

-- BranchHead finds the object a branch is at, deleted or not.
-- name: BranchHead :one
SELECT head FROM branches WHERE id = $1;

-- BranchDeleted reports whether a branch was deleted.
-- name: BranchDeleted :one
SELECT (deleted_at IS NOT NULL)::boolean AS deleted FROM branches WHERE id = $1;
