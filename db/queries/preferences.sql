-- Preferences reads a principal's preferences; none until they first change one.
-- name: Preferences :one
SELECT * FROM preferences WHERE principal_id = @principal_id;

-- InsertPreferences writes a principal's first preferences, or nothing when another change wrote them first.
-- name: InsertPreferences :one
INSERT INTO preferences (principal_id, theme, updated_at) VALUES (@principal_id, @theme, @updated_at)
ON CONFLICT (principal_id) DO NOTHING
RETURNING *;

-- UpdatePreferences changes a principal's preferences, or nothing when they are no longer at version.
-- name: UpdatePreferences :one
UPDATE preferences SET theme = @theme, updated_at = @updated_at WHERE principal_id = @principal_id AND version = @version
RETURNING *;
