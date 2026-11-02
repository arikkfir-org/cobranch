-- Issue comments: an issue's comments are a chat, as a branch's are, whose threads name the issue instead of a branch
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/issues.md, "Comments"). Each change to an
-- issue's chat appends an event whose subject is the issue, which orders its live updates and goes with it.
-- +goose Up

-- A thread is of a branch's chat, in the branch's repository, or of an issue's, which has neither a repository nor an
-- anchor: an issue is the organization's, and its comments point at no file.
ALTER TABLE chat_threads
    ALTER COLUMN repo_id DROP NOT NULL,
    ALTER COLUMN branch_id DROP NOT NULL,
    ADD COLUMN issue_id uuid REFERENCES issues (id) ON DELETE CASCADE,
    ADD CONSTRAINT chat_threads_subject CHECK (num_nonnulls(branch_id, issue_id) = 1),
    ADD CONSTRAINT chat_threads_branch_repo CHECK ((branch_id IS NULL) = (repo_id IS NULL)),
    ADD CONSTRAINT chat_threads_issue_unanchored CHECK (issue_id IS NULL OR sha IS NULL);
CREATE INDEX chat_threads_issue ON chat_threads (issue_id, id) WHERE issue_id IS NOT NULL;

-- +goose Down
-- Rolling back forgets the issues' chats and their events; the audit log keeps what they said.
DELETE FROM events WHERE kind LIKE 'chat.%' AND subject_id IN (SELECT id FROM issues);
DELETE FROM chat_threads WHERE issue_id IS NOT NULL;
DROP INDEX chat_threads_issue;
ALTER TABLE chat_threads
    DROP CONSTRAINT chat_threads_issue_unanchored,
    DROP CONSTRAINT chat_threads_branch_repo,
    DROP CONSTRAINT chat_threads_subject,
    DROP COLUMN issue_id,
    ALTER COLUMN branch_id SET NOT NULL,
    ALTER COLUMN repo_id SET NOT NULL;
