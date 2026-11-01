-- name: InsertSSHKey :exec
INSERT INTO credentials (id, kind, token_hash, principal_id, hint, created_at, created_by, name, public_key)
VALUES (@id, 'ssh_key', @token_hash, @principal_id, @hint, @created_at, @principal_id, @name, @public_key);

-- SSHKeys lists the user's SSH keys, newest first.
-- name: SSHKeys :many
SELECT * FROM credentials WHERE principal_id = @principal_id AND kind = 'ssh_key' ORDER BY created_at DESC, id;

-- name: DeleteSSHKey :execrows
DELETE FROM credentials WHERE id = @id AND principal_id = @principal_id AND kind = 'ssh_key';
