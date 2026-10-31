-- Repositories: an organization's Git repositories, their slugs (the current one and the old ones a rename keeps as
-- redirects), and the grants at their scope
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/git-hosting.md, "Repositories"). Their
-- refs, packs and pushes come with the Git server. Every row a repository owns carries its org_id.
-- +goose Up

-- A repository: a slug for its URLs, unique in its organization, a name, a description, the branch it opens on, and
-- the object format of its history, so SHA-256 repositories need no migration of the data.
CREATE TABLE repositories (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    slug text NOT NULL,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    default_branch text NOT NULL DEFAULT 'main',
    object_format text NOT NULL DEFAULT 'sha1' CHECK (object_format IN ('sha1', 'sha256')),
    created_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);

-- Every slug a repository has had in its organization, the current one included: an old one redirects to its
-- repository until another repository of the organization takes it.
CREATE TABLE repository_slugs (
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    slug text NOT NULL,
    repo_id uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, slug)
);
CREATE INDEX repository_slugs_repo ON repository_slugs (repo_id);

CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON repositories FOR EACH ROW EXECUTE FUNCTION audit();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON repository_slugs FOR EACH ROW EXECUTE FUNCTION audit();

-- +goose Down
-- Rolling back drops the repositories with the grants at their scope: the previous schema has no room for them.
DELETE FROM grants WHERE scope = 'repository';
DROP TABLE repository_slugs;
DROP TABLE repositories;
