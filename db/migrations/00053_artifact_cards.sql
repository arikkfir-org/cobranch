-- Artifact cards: a chat message may name one of its organization's artifacts, which the conversation draws as a card
-- that opens it, as an agent's harness posts one for each artifact it writes
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/artifacts.md#generate). Deleting the
-- artifact leaves the message, without its card.
-- +goose Up
ALTER TABLE chat_messages ADD COLUMN artifact_id uuid REFERENCES artifacts (id) ON DELETE SET NULL;
CREATE INDEX chat_messages_artifact ON chat_messages (artifact_id) WHERE artifact_id IS NOT NULL;

-- +goose Down
DROP INDEX chat_messages_artifact;
ALTER TABLE chat_messages DROP COLUMN artifact_id;
