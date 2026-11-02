-- Secrets: the outside credentials an organization's settings keep for its repositories, and a repository's for its
-- own, which jobs receive when their grants allow
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md, "Secrets"). A secret is also a
-- scope of its own, for grants of secret permissions only, and role Secret reader holds secrets.read alone.
-- +goose Up

-- A secret: a name for people, unique where it lives, and a value sealed with the installation's secrets key for
-- this row alone, which nothing reads back but a run or a task that may receive it. repo_id is NULL for the
-- organization's own. Its version is its ETag.
CREATE TABLE secrets (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    repo_id uuid REFERENCES repositories (id) ON DELETE CASCADE,
    name text NOT NULL,
    value bytea NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (org_id, repo_id, name)
);
CREATE INDEX secrets_repo ON secrets (repo_id);
CREATE TRIGGER bump_version BEFORE UPDATE ON secrets FOR EACH ROW EXECUTE FUNCTION bump_version();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON secrets FOR EACH ROW EXECUTE FUNCTION audit('value');

-- A grant may name one secret, which reaches only it.
ALTER TABLE grants DROP CONSTRAINT grants_scope_check;
ALTER TABLE grants ADD CONSTRAINT grants_scope_check CHECK (scope IN ('installation', 'organization', 'repository', 'secret'));

-- +goose StatementBegin
-- delete_secret_grants takes the grants on a secret with it, however it goes: deleted, or with its repository or
-- organization.
CREATE FUNCTION delete_secret_grants() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM grants WHERE scope = 'secret' AND scope_id = OLD.id;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER delete_grants AFTER DELETE ON secrets FOR EACH ROW EXECUTE FUNCTION delete_secret_grants();

-- Secret reader: a repository role, so a repository grants it, and the one role a secret grants.
INSERT INTO roles (id, scope, name, description, permissions, built_in) VALUES
    ('00000000-0000-7000-8000-000000000305', 'repository', 'Secret reader',
     'Receives the secrets where it is granted, in its job runs and agent tasks; reads nothing else.', '{secrets.read}', true)
ON CONFLICT (id) DO NOTHING;

-- +goose Down
DELETE FROM grants WHERE scope = 'secret';
DELETE FROM roles WHERE id = '00000000-0000-7000-8000-000000000305';
ALTER TABLE grants DROP CONSTRAINT grants_scope_check;
ALTER TABLE grants ADD CONSTRAINT grants_scope_check CHECK (scope IN ('installation', 'organization', 'repository'));
DROP TABLE secrets;
DROP FUNCTION delete_secret_grants();
