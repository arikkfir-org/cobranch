-- SSH keys: credentials a user adds from a session, which act as the user over SSH, unnarrowed, as their session would
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/git-hosting.md, "SSH").
-- +goose Up

-- An SSH key is a credential of kind ssh_key on a user: a name and its public key, found by the SHA-256 of the key's
-- wire form (token_hash), so a key names one user; its hint is the key's fingerprint, and it never expires. Deleting
-- one deletes its row, so its user may add the key again.
ALTER TABLE credentials DROP CONSTRAINT credentials_kind_check;
ALTER TABLE credentials ADD CONSTRAINT credentials_kind_check CHECK (kind IN ('api_key', 'personal_token', 'ssh_key'));
ALTER TABLE credentials ADD COLUMN public_key text NOT NULL DEFAULT '';
ALTER TABLE credentials DROP CONSTRAINT credentials_personal_token;
ALTER TABLE credentials ADD CONSTRAINT credentials_kinds CHECK (
    (kind = 'api_key' AND name = '' AND permissions IS NULL AND issuer = '' AND subject = '' AND public_key = '')
    OR (kind = 'personal_token' AND name <> '' AND permissions IS NOT NULL AND issuer <> '' AND subject <> '' AND expires_at IS NOT NULL
        AND public_key = '')
    OR (kind = 'ssh_key' AND name <> '' AND permissions IS NULL AND issuer = '' AND subject = '' AND expires_at IS NULL
        AND public_key <> '')
);

-- +goose Down
-- Rolling back deletes the SSH keys: the previous schema has no room for them.
DELETE FROM credentials WHERE kind = 'ssh_key';
ALTER TABLE credentials DROP CONSTRAINT credentials_kinds;
ALTER TABLE credentials ADD CONSTRAINT credentials_personal_token CHECK (
    (kind = 'api_key' AND name = '' AND permissions IS NULL AND issuer = '' AND subject = '')
    OR (kind = 'personal_token' AND name <> '' AND permissions IS NOT NULL AND issuer <> '' AND subject <> '' AND expires_at IS NOT NULL)
);
ALTER TABLE credentials DROP COLUMN public_key;
ALTER TABLE credentials DROP CONSTRAINT credentials_kind_check;
ALTER TABLE credentials ADD CONSTRAINT credentials_kind_check CHECK (kind IN ('api_key', 'personal_token'));
