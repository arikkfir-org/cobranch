-- InternalToken reads the token of an internal call by its name, such as git-server.
-- name: InternalToken :one
SELECT token FROM internal_tokens WHERE name = $1;
