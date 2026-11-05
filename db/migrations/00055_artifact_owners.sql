-- Artifacts belong to people: each has an owner, the person who asked for it, and is attached to a repository or, when
-- asked for in an issue's or the organization's conversation, to none. artifacts.list joins the artifact permissions,
-- and a grant may name a person, whose artifacts it reaches
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/artifacts.md#access).
-- +goose Up

-- An artifact's owner: the person who asked for it, or the agent of a task Cobranch started. One already there belongs
-- to whoever its first Artifact owner grant names, or to its maker.
ALTER TABLE artifacts ADD COLUMN owner_id uuid REFERENCES principals (id) ON DELETE SET NULL;
UPDATE artifacts a SET owner_id = coalesce((
    SELECT g.principal_id FROM grants g
    WHERE g.scope = 'artifact' AND g.scope_id = a.id AND g.role_id = '00000000-0000-7000-8000-000000000308'
    ORDER BY g.created_at, g.id LIMIT 1), a.created_by);
CREATE INDEX artifacts_owner ON artifacts (owner_id);

-- An artifact attached to no repository has no repository audience.
ALTER TABLE artifacts ALTER COLUMN repo_id DROP NOT NULL;
ALTER TABLE artifacts ADD CONSTRAINT artifacts_repository_access CHECK (repo_id IS NOT NULL OR repository_access = 'none');

-- A grant may name a person, which reaches the artifacts they own.
ALTER TABLE grants DROP CONSTRAINT grants_scope_check;
ALTER TABLE grants ADD CONSTRAINT grants_scope_check CHECK (scope IN ('installation', 'organization', 'repository', 'secret', 'artifact', 'user'));

-- +goose StatementBegin
-- delete_user_grants takes the grants on a person with them when they go.
CREATE FUNCTION delete_user_grants() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM grants WHERE scope = 'user' AND scope_id = OLD.id;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER delete_user_grants AFTER DELETE ON principals FOR EACH ROW EXECUTE FUNCTION delete_user_grants();

-- +goose StatementBegin
-- artifact_permissions lists the permissions principal holds on artifact, once for each grant or audience that gives
-- it: those of its grants on the artifact; on its repository, or at its organization, when it is attached to one; and,
-- while principal reads its organization, artifacts.list and artifacts.read when principal owns it, or holds them on
-- its owner. Its audiences give a principal reading its repository, or its organization, artifacts.read for view and
-- artifacts.write besides for edit. Every check of an artifact reads it, so a list and an artifact's page agree.
CREATE FUNCTION artifact_permissions(principal uuid, artifact uuid) RETURNS TABLE (permission text) LANGUAGE sql STABLE AS $$
    SELECT unnest(r.permissions) FROM grants g JOIN roles r ON r.id = g.role_id
    WHERE g.principal_id = principal AND g.scope = 'artifact' AND g.scope_id = artifact
    UNION ALL
    SELECT unnest(r.permissions) FROM artifacts a
    JOIN grants g ON g.principal_id = principal AND a.repo_id IS NOT NULL
        AND ((g.scope = 'repository' AND g.scope_id = a.repo_id) OR (g.scope = 'organization' AND g.scope_id = a.org_id))
    JOIN roles r ON r.id = g.role_id
    WHERE a.id = artifact
    UNION ALL
    SELECT o.permission FROM artifacts a
    CROSS JOIN LATERAL (
        SELECT unnest('{artifacts.list,artifacts.read}'::text[]) AS permission WHERE a.owner_id = principal
        UNION ALL
        SELECT unnest(r.permissions) FROM grants g JOIN roles r ON r.id = g.role_id
        WHERE g.principal_id = principal AND g.scope = 'user' AND g.scope_id = a.owner_id
    ) o
    WHERE a.id = artifact AND o.permission IN ('artifacts.list', 'artifacts.read') AND EXISTS (
        SELECT 1 FROM grants g JOIN roles r ON r.id = g.role_id
        WHERE g.principal_id = principal AND g.scope = 'organization' AND g.scope_id = a.org_id AND 'org.read' = ANY (r.permissions))
    UNION ALL
    SELECT unnest(CASE x.access WHEN 'edit' THEN '{artifacts.read,artifacts.write}'::text[] ELSE '{artifacts.read}'::text[] END)
    FROM (
        SELECT a.repository_access AS access FROM artifacts a
        WHERE a.id = artifact AND a.repo_id IS NOT NULL AND a.repository_access <> 'none' AND EXISTS (
            SELECT 1 FROM grants g JOIN roles r ON r.id = g.role_id
            WHERE g.principal_id = principal AND 'repo.read' = ANY (r.permissions)
              AND ((g.scope = 'repository' AND g.scope_id = a.repo_id) OR (g.scope = 'organization' AND g.scope_id = a.org_id)))
        UNION ALL
        SELECT a.organization_access FROM artifacts a
        WHERE a.id = artifact AND a.organization_access <> 'none' AND EXISTS (
            SELECT 1 FROM grants g JOIN roles r ON r.id = g.role_id
            WHERE g.principal_id = principal AND g.scope = 'organization' AND g.scope_id = a.org_id AND 'org.read' = ANY (r.permissions))
    ) x
$$;
-- +goose StatementEnd

-- Artifact lister: a repository role, granted on a repository, at the organization by a role of its own, or on a
-- person by that person.
INSERT INTO roles (id, scope, name, description, permissions, built_in) VALUES
    ('00000000-0000-7000-8000-000000000309', 'repository', 'Artifact lister',
     'Lists the artifacts where it is granted, without opening them.', '{artifacts.list}', true)
ON CONFLICT (id) DO NOTHING;

-- Owner and Maintainer list every artifact they reach. Appended, never set, so other slices' additions stay.
UPDATE roles SET permissions = permissions || '{artifacts.list}'
WHERE id IN ('00000000-0000-7000-8000-000000000201', '00000000-0000-7000-8000-000000000303')
  AND NOT permissions @> '{artifacts.list}';

-- +goose Down
-- Rolling back forgets the grants on people and artifacts.list, and the artifacts attached to no repository; the
-- audit log keeps what they were, their bodies aside.
DELETE FROM grants WHERE scope = 'user' OR role_id = '00000000-0000-7000-8000-000000000309';
DELETE FROM roles WHERE id = '00000000-0000-7000-8000-000000000309';
UPDATE roles SET permissions = array_remove(permissions, 'artifacts.list');
DROP FUNCTION artifact_permissions(uuid, uuid);
DROP TRIGGER delete_user_grants ON principals;
DROP FUNCTION delete_user_grants();
ALTER TABLE grants DROP CONSTRAINT grants_scope_check;
ALTER TABLE grants ADD CONSTRAINT grants_scope_check CHECK (scope IN ('installation', 'organization', 'repository', 'secret', 'artifact'));
DELETE FROM artifacts WHERE repo_id IS NULL;
ALTER TABLE artifacts DROP CONSTRAINT artifacts_repository_access;
ALTER TABLE artifacts ALTER COLUMN repo_id SET NOT NULL;
DROP INDEX artifacts_owner;
ALTER TABLE artifacts DROP COLUMN owner_id;
