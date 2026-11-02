-- Review comments: a review's comments are threads of the branch's chat, anchored to lines of the reviewed commit or
-- of its diff's base (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/reviews.md,
-- "Comments").
-- +goose Up

-- The review a thread is a comment of, which opened it in its transaction; null for a thread somebody started in the
-- chat, and once its review is gone, when the thread stays in the chat with its answers. A comment is anchored.
ALTER TABLE chat_threads
    ADD COLUMN review_id uuid REFERENCES reviews (id) ON DELETE SET NULL,
    ADD CONSTRAINT chat_threads_review_anchored CHECK (review_id IS NULL OR sha IS NOT NULL);
CREATE INDEX chat_threads_review ON chat_threads (review_id) WHERE review_id IS NOT NULL;

-- +goose Down
-- Rolling back keeps the comments as threads of their chats, no longer tied to their reviews; the column's index and
-- check go with it.
ALTER TABLE chat_threads DROP COLUMN review_id;
