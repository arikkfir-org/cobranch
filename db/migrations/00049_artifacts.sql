-- Artifacts: documents, diagrams, reports and pages that agents write when asked, owned by a repository, kept in
-- versions, and reached through grants on the artifact, its repository and its organization
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/artifacts.md). An artifact is a scope of
-- its own, for grants of artifact permissions only, as a secret is.
-- +goose Up

-- An artifact: a repository's, with what it is for, its title and summary, and its two audiences, the repository's
-- readers and the organization's members, each reaching none, view or edit. agent_id, task_id and branch say where it
-- came from; head is its newest version's number. Its version is its ETag.
CREATE TABLE artifacts (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    repo_id uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    summary text NOT NULL DEFAULT '' CHECK (char_length(summary) <= 500),
    kind text NOT NULL CHECK (kind IN ('document', 'diagram', 'report', 'page')),
    repository_access text NOT NULL DEFAULT 'none' CHECK (repository_access IN ('none', 'view', 'edit')),
    organization_access text NOT NULL DEFAULT 'none' CHECK (organization_access IN ('none', 'view', 'edit')),
    agent_id uuid REFERENCES principals (id) ON DELETE SET NULL,
    task_id uuid REFERENCES agent_tasks (id) ON DELETE SET NULL,
    branch text NOT NULL DEFAULT '',
    head integer NOT NULL DEFAULT 1 CHECK (head >= 1),
    version bigint NOT NULL DEFAULT 1,
    created_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX artifacts_repo ON artifacts (repo_id, updated_at DESC);
CREATE INDEX artifacts_org ON artifacts (org_id);
CREATE TRIGGER bump_version BEFORE UPDATE ON artifacts FOR EACH ROW EXECUTE FUNCTION bump_version();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON artifacts FOR EACH ROW EXECUTE FUNCTION audit();

-- A version of an artifact, never changed: its number from 1, its ID (the call that wrote it, so a call run twice
-- writes it once), its format and body, who wrote it, from which task, and why. Its body stays out of the audit log.
CREATE TABLE artifact_versions (
    artifact_id uuid NOT NULL REFERENCES artifacts (id) ON DELETE CASCADE,
    number integer NOT NULL CHECK (number >= 1),
    id uuid NOT NULL UNIQUE,
    media_type text NOT NULL CHECK (media_type IN ('text/markdown', 'image/svg+xml', 'text/html')),
    body text NOT NULL CHECK (octet_length(body) <= 1048576),
    author_id uuid REFERENCES principals (id) ON DELETE SET NULL,
    task_id uuid REFERENCES agent_tasks (id) ON DELETE SET NULL,
    note text NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (artifact_id, number)
);
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON artifact_versions FOR EACH ROW EXECUTE FUNCTION audit('body');

-- A grant may name one artifact, which reaches only it.
ALTER TABLE grants DROP CONSTRAINT grants_scope_check;
ALTER TABLE grants ADD CONSTRAINT grants_scope_check CHECK (scope IN ('installation', 'organization', 'repository', 'secret', 'artifact'));

-- +goose StatementBegin
-- delete_artifact_grants takes the grants on an artifact with it, however it goes: deleted, or with its repository or
-- organization.
CREATE FUNCTION delete_artifact_grants() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM grants WHERE scope = 'artifact' AND scope_id = OLD.id;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER delete_grants AFTER DELETE ON artifacts FOR EACH ROW EXECUTE FUNCTION delete_artifact_grants();

-- The artifact roles: repository roles, so a repository grants them for all of its artifacts, and the roles an
-- artifact grants.
INSERT INTO roles (id, scope, name, description, permissions, built_in) VALUES
    ('00000000-0000-7000-8000-000000000306', 'repository', 'Artifact viewer',
     'Views the artifacts where it is granted, their versions and who reaches them.', '{artifacts.read}', true),
    ('00000000-0000-7000-8000-000000000307', 'repository', 'Artifact editor',
     'Views the artifacts where it is granted and adds their versions.', '{artifacts.read,artifacts.write}', true),
    ('00000000-0000-7000-8000-000000000308', 'repository', 'Artifact owner',
     'Views, edits, shares and deletes the artifacts where it is granted.', '{artifacts.read,artifacts.write,artifacts.manage}', true)
ON CONFLICT (id) DO NOTHING;

-- The artifact permissions join the built-in roles: Owner and Maintainer hold all four, Developer creates them.
-- Appended, never set, so other slices' additions stay.
UPDATE roles SET permissions = permissions || '{artifacts.create,artifacts.read,artifacts.write,artifacts.manage}'
WHERE id IN ('00000000-0000-7000-8000-000000000201', '00000000-0000-7000-8000-000000000303')
  AND NOT permissions && '{artifacts.create,artifacts.read,artifacts.write,artifacts.manage}';
UPDATE roles SET permissions = permissions || '{artifacts.create}'
WHERE id = '00000000-0000-7000-8000-000000000302' AND NOT permissions @> '{artifacts.create}';

-- +goose Down
-- Rolling back forgets every artifact, the grants on them and the artifact permissions of every role; the audit log
-- keeps what they were, their bodies aside.
DELETE FROM grants WHERE scope = 'artifact' OR role_id IN ('00000000-0000-7000-8000-000000000306',
    '00000000-0000-7000-8000-000000000307', '00000000-0000-7000-8000-000000000308');
DELETE FROM roles WHERE id IN ('00000000-0000-7000-8000-000000000306', '00000000-0000-7000-8000-000000000307',
    '00000000-0000-7000-8000-000000000308');
UPDATE roles SET permissions = array_remove(array_remove(array_remove(array_remove(permissions,
    'artifacts.create'), 'artifacts.read'), 'artifacts.write'), 'artifacts.manage');
ALTER TABLE grants DROP CONSTRAINT grants_scope_check;
ALTER TABLE grants ADD CONSTRAINT grants_scope_check CHECK (scope IN ('installation', 'organization', 'repository', 'secret'));
DROP TABLE artifact_versions;
DROP TABLE artifacts;
DROP FUNCTION delete_artifact_grants();
