-- ChatSubject finds a repository's branch by name, as the subject of its chat: the live one, or else the one of that
-- name deleted last, with the commit it is at and the slugs of the repository and its organization.
-- name: ChatSubject :one
SELECT b.id, b.org_id, b.repo_id, b.name, b.head, r.slug AS repo_slug, o.slug AS org_slug
FROM branches b JOIN repositories r ON r.id = b.repo_id JOIN organizations o ON o.id = b.org_id
WHERE b.repo_id = $1 AND b.name = $2
ORDER BY b.deleted_at DESC NULLS FIRST LIMIT 1;

-- IssueChatSubject finds one of an organization's issues by number, as the subject of its chat, with its
-- organization's slug and issue key.
-- name: IssueChatSubject :one
SELECT i.id, i.org_id, i.number, o.slug AS org_slug, o.issue_key
FROM issues i JOIN organizations o ON o.id = i.org_id
WHERE i.org_id = $1 AND i.number = $2;

-- OrganizationConversationSubject finds an organization, as the subject of a person's conversation about it, with its
-- slug.
-- name: OrganizationConversationSubject :one
SELECT id, slug FROM organizations WHERE id = $1;

-- RepositoryConversationSubject finds one of an organization's repositories, as the subject of a person's conversation
-- about it, with its slug and its organization's, and the commit its live default branch is at, if it has one.
-- name: RepositoryConversationSubject :one
SELECT r.id, r.org_id, r.slug AS repo_slug, o.slug AS org_slug, b.head
FROM repositories r JOIN organizations o ON o.id = r.org_id
    LEFT JOIN branches b ON b.repo_id = r.id AND b.name = @default_branch AND b.deleted_at IS NULL
WHERE r.org_id = @org_id AND r.id = @repo_id;

-- AgentConversationSubject finds one of an organization's agents, as the subject of a person's conversation with it,
-- with its name and its organization's slug.
-- name: AgentConversationSubject :one
SELECT a.principal_id, a.org_id, a.name, o.slug AS org_slug
FROM agents a JOIN organizations o ON o.id = a.org_id
WHERE a.org_id = $1 AND a.principal_id = $2;

-- EnsureConversation makes a person's conversation about a subject, unless it is there already.
-- name: EnsureConversation :exec
INSERT INTO conversations (id, org_id, owner_id, repo_id, agent_id)
VALUES (@id, @org_id, @owner_id, sqlc.narg(repo_id), sqlc.narg(agent_id))
ON CONFLICT DO NOTHING;

-- HoldConversation finds a conversation and keeps it from being deleted until the transaction ends, so that a change to
-- it doesn't race the deletion of its subject or its owner.
-- name: HoldConversation :one
SELECT id FROM conversations WHERE id = $1 FOR KEY SHARE;

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

-- ChatThreads lists at most lim of the chat threads of an organization's subject, a branch, an issue or a conversation,
-- newest first, from the one before before names, or from the newest when it is null, with the display names of their
-- starters and resolvers, and the commit each one's review reviewed.
-- name: ChatThreads :many
SELECT sqlc.embed(t), s.display_name AS starter_name, rb.display_name AS resolver_name, rv.sha AS reviewed_sha
FROM chat_threads t LEFT JOIN principals s ON s.id = t.created_by LEFT JOIN principals rb ON rb.id = t.resolved_by
LEFT JOIN reviews rv ON rv.id = t.review_id
WHERE t.org_id = @org_id AND (t.branch_id = @subject_id::uuid OR t.issue_id = @subject_id::uuid OR t.conversation_id = @subject_id::uuid)
  AND (sqlc.narg(before)::uuid IS NULL OR t.id < sqlc.narg(before)::uuid)
ORDER BY t.id DESC LIMIT @lim;

-- ChatThreadsByID finds those of the chat threads of an organization's subject, a branch, an issue or a conversation,
-- that ids name, with the display names of their starters and resolvers, and the commit each one's review reviewed.
-- name: ChatThreadsByID :many
SELECT sqlc.embed(t), s.display_name AS starter_name, rb.display_name AS resolver_name, rv.sha AS reviewed_sha
FROM chat_threads t LEFT JOIN principals s ON s.id = t.created_by LEFT JOIN principals rb ON rb.id = t.resolved_by
LEFT JOIN reviews rv ON rv.id = t.review_id
WHERE t.org_id = @org_id AND (t.branch_id = @subject_id::uuid OR t.issue_id = @subject_id::uuid OR t.conversation_id = @subject_id::uuid) AND t.id = ANY (@ids::uuid[]);

-- ChatMessagesOf lists the messages of the threads that thread_ids name, by thread and then in the order they were
-- posted, with the display names of their authors and deleters, and the number of the task each came from.
-- name: ChatMessagesOf :many
SELECT sqlc.embed(m), a.display_name AS author_name, d.display_name AS deleter_name, tk.number AS task_number
FROM chat_messages m LEFT JOIN principals a ON a.id = m.author_id LEFT JOIN principals d ON d.id = m.deleted_by
    LEFT JOIN agent_tasks tk ON tk.id = m.task_id
WHERE m.thread_id = ANY (@thread_ids::uuid[])
ORDER BY m.thread_id, m.created_at, m.id;

-- ChatMessagesByID finds the messages that ids name, with the display names of their authors and deleters, and the
-- number of the task each came from.
-- name: ChatMessagesByID :many
SELECT sqlc.embed(m), a.display_name AS author_name, d.display_name AS deleter_name, tk.number AS task_number
FROM chat_messages m LEFT JOIN principals a ON a.id = m.author_id LEFT JOIN principals d ON d.id = m.deleted_by
    LEFT JOIN agent_tasks tk ON tk.id = m.task_id
WHERE m.id = ANY (@ids::uuid[]);

-- InsertChatThread starts a thread of a branch's chat, in its repository, of an issue's or of a conversation, perhaps a
-- comment of a review: none when one of its ID is there already.
-- name: InsertChatThread :one
INSERT INTO chat_threads (id, org_id, repo_id, branch_id, issue_id, conversation_id, sha, path, start_line, end_line, created_by, review_id)
VALUES (@id, @org_id, sqlc.narg(repo_id), sqlc.narg(branch_id), sqlc.narg(issue_id), sqlc.narg(conversation_id), sqlc.narg(sha), sqlc.narg(path),
        sqlc.narg(start_line), sqlc.narg(end_line), @created_by, sqlc.narg(review_id))
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- InsertChatMessage posts a message, perhaps from an agent's task: none when one of its ID is there already.
-- name: InsertChatMessage :one
INSERT INTO chat_messages (id, thread_id, org_id, author_id, body, task_id)
VALUES (@id, @thread_id, @org_id, @author_id, @body, sqlc.narg(task_id))
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- SetTaskThread makes a thread the one a task talks in, when it talks in none yet and the thread is of its branch's
-- chat, or of its issue's for a task without a branch.
-- name: SetTaskThread :exec
UPDATE agent_tasks t SET thread_id = th.id
FROM chat_threads th
WHERE t.id = @task_id AND t.thread_id IS NULL AND th.id = @thread_id
    AND (th.branch_id = t.branch_id OR (t.branch_id IS NULL AND th.issue_id = t.issue_id));

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

-- TouchChatThread bumps a thread's version, its ETag, for a change to its messages.
-- name: TouchChatThread :exec
UPDATE chat_threads SET version = version + 1 WHERE id = @id;

-- BranchHead finds the object a branch is at, deleted or not.
-- name: BranchHead :one
SELECT head FROM branches WHERE id = $1;

-- BranchDeleted reports whether a branch was deleted.
-- name: BranchDeleted :one
SELECT (deleted_at IS NOT NULL)::boolean AS deleted FROM branches WHERE id = $1;

-- HoldIssue finds an issue and keeps it from being deleted until the transaction ends, so that a change to its chat
-- doesn't race its deletion.
-- name: HoldIssue :one
SELECT id FROM issues WHERE id = $1 FOR KEY SHARE;
