-- name: InsertInvitation :exec
INSERT INTO invitations (id, org_id, email, role_id, invited_by, created_at, expires_at)
VALUES (@id, @org_id, @email, @role_id, @invited_by, @created_at, @expires_at);

-- Invitation finds an invitation with its organization's slug and name.
-- name: Invitation :one
SELECT sqlc.embed(i), o.slug, o.name FROM invitations i JOIN organizations o ON o.id = i.org_id WHERE i.id = $1;

-- PendingInvitations lists an organization's invitations that wait, oldest first.
-- name: PendingInvitations :many
SELECT sqlc.embed(i), o.slug, o.name FROM invitations i JOIN organizations o ON o.id = i.org_id
WHERE i.org_id = $1 AND i.state = 'pending' AND i.expires_at > @now::timestamptz ORDER BY i.created_at, i.id;

-- InvitationsFor lists the invitations that wait for any of the emails, oldest first.
-- name: InvitationsFor :many
SELECT sqlc.embed(i), o.slug, o.name FROM invitations i JOIN organizations o ON o.id = i.org_id
WHERE i.email = ANY(@emails::text[]) AND i.state = 'pending' AND i.expires_at > @now::timestamptz ORDER BY i.created_at, i.id;

-- CloseInvitation ends a pending invitation.
-- name: CloseInvitation :execrows
UPDATE invitations SET state = @state, decided_at = @decided_at, decided_by = @decided_by WHERE id = @id AND state = 'pending';

-- name: DeleteExpiredInvitations :execrows
DELETE FROM invitations WHERE state = 'pending' AND expires_at < @now::timestamptz;
