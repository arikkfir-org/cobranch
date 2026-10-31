-- name: InsertJoinRequest :exec
INSERT INTO join_requests (id, org_id, user_id, note, created_at, expires_at)
VALUES (@id, @org_id, @user_id, @note, @created_at, @expires_at);

-- JoinRequest finds a join request with its organization's slug and name and its user's display name.
-- name: JoinRequest :one
SELECT sqlc.embed(j), o.slug, o.name, p.display_name FROM join_requests j
    JOIN organizations o ON o.id = j.org_id JOIN principals p ON p.id = j.user_id
WHERE j.id = $1;

-- PendingJoinRequests lists the requests waiting at an organization, oldest first.
-- name: PendingJoinRequests :many
SELECT sqlc.embed(j), o.slug, o.name, p.display_name FROM join_requests j
    JOIN organizations o ON o.id = j.org_id JOIN principals p ON p.id = j.user_id
WHERE j.org_id = $1 AND j.state = 'pending' AND j.expires_at > @now::timestamptz ORDER BY j.created_at, j.id;

-- JoinRequestsOf lists a user's requests that wait, oldest first.
-- name: JoinRequestsOf :many
SELECT sqlc.embed(j), o.slug, o.name, p.display_name FROM join_requests j
    JOIN organizations o ON o.id = j.org_id JOIN principals p ON p.id = j.user_id
WHERE j.user_id = $1 AND j.state = 'pending' AND j.expires_at > @now::timestamptz ORDER BY j.created_at, j.id;

-- CloseJoinRequest ends a pending request.
-- name: CloseJoinRequest :execrows
UPDATE join_requests SET state = @state, decided_at = @decided_at, decided_by = @decided_by WHERE id = @id AND state = 'pending';

-- name: DeleteExpiredJoinRequests :execrows
DELETE FROM join_requests WHERE state = 'pending' AND expires_at < @now::timestamptz;

-- DeleteUserJoinRequests drops the user's pending requests at the organization: moot once they joined, and not to
-- outlive their leaving.
-- name: DeleteUserJoinRequests :execrows
DELETE FROM join_requests WHERE org_id = $1 AND user_id = $2 AND state = 'pending';

-- DeleteExpiredJoinRequestOf drops the user's expired request that would block a new one.
-- name: DeleteExpiredJoinRequestOf :execrows
DELETE FROM join_requests WHERE org_id = $1 AND user_id = $2 AND state = 'pending' AND expires_at <= @now::timestamptz;
