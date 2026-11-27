-- ListenChanges has the connection hear the notifications that migration 00066's triggers send when a row a Console
-- page shows changes, naming what it belongs to (an organization).
-- name: ListenChanges :exec
LISTEN cobranch_changes;
