-- Personal tokens: credentials a user makes from a session, which act as the user, narrowed to the permissions they
-- list, and stand in for a password where a Git client asks for one
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/git-hosting.md, "Personal tokens").
-- +goose Up

-- A personal token is a credential of kind personal_token on a user: a name, the permissions it may use, the identity
-- of the session that made it, and an end, which it can't go without. An API key's secret has none of the first three.
ALTER TABLE credentials DROP CONSTRAINT credentials_kind_check;
ALTER TABLE credentials ADD CONSTRAINT credentials_kind_check CHECK (kind IN ('api_key', 'personal_token'));
ALTER TABLE credentials ADD COLUMN name text NOT NULL DEFAULT '';
ALTER TABLE credentials ADD COLUMN permissions text[];
ALTER TABLE credentials ADD COLUMN issuer text NOT NULL DEFAULT '';
ALTER TABLE credentials ADD COLUMN subject text NOT NULL DEFAULT '';
ALTER TABLE credentials ADD CONSTRAINT credentials_personal_token CHECK (
    (kind = 'api_key' AND name = '' AND permissions IS NULL AND issuer = '' AND subject = '')
    OR (kind = 'personal_token' AND name <> '' AND permissions IS NOT NULL AND issuer <> '' AND subject <> '' AND expires_at IS NOT NULL)
);

-- +goose Down
-- Rolling back deletes the personal tokens: the previous schema has no room for them.
DELETE FROM credentials WHERE kind = 'personal_token';
ALTER TABLE credentials DROP CONSTRAINT credentials_personal_token;
ALTER TABLE credentials DROP COLUMN subject;
ALTER TABLE credentials DROP COLUMN issuer;
ALTER TABLE credentials DROP COLUMN permissions;
ALTER TABLE credentials DROP COLUMN name;
ALTER TABLE credentials DROP CONSTRAINT credentials_kind_check;
ALTER TABLE credentials ADD CONSTRAINT credentials_kind_check CHECK (kind IN ('api_key'));
