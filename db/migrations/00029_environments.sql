-- Delivery: each target a repository's /cobranch.yaml names is a principal of its own, an environment per target and
-- live branch, and the deliveries that pushes and members queue for each
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/delivery.md). The push worker queues them;
-- the controller delivers them.
-- +goose Up

-- A target is a principal of its repository's organization, as a job is. The kinds are 00021's, and target.
ALTER TABLE principals DROP CONSTRAINT principals_kind_check;
ALTER TABLE principals ADD CONSTRAINT principals_kind_check
    CHECK (kind IN ('user', 'anonymous', 'agent', 'automation', 'installation', 'api_key', 'job', 'target'));
ALTER TABLE principals DROP CONSTRAINT principals_org_kind;
ALTER TABLE principals ADD CONSTRAINT principals_org_kind
    CHECK ((kind IN ('api_key', 'job', 'agent', 'target')) = (org_id IS NOT NULL));

-- A target of a repository, by name, made on the first delivery to it and kept across them: its annotation on a
-- commit, delivery/<name>, is its own. A renamed target is a new principal; the old one keeps its history.
CREATE TABLE delivery_targets (
    principal_id uuid PRIMARY KEY REFERENCES principals (id) ON DELETE CASCADE,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    repo_id uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    name text NOT NULL,
    UNIQUE (repo_id, name)
);

-- An environment: a target and a branch, delivered to the target's cluster (the secret that holds its kubeconfig, or
-- null for the installation's own) in a namespace of its own. revision is the commit of its last successful delivery;
-- sync, whether the cluster holds it; health, its objects' worst. The controller keeps them, so they aren't audited;
-- events record who paused or resumed one. Each change bumps its version, its ETag.
CREATE TABLE environments (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    repo_id uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    target_id uuid NOT NULL REFERENCES delivery_targets (principal_id) ON DELETE CASCADE,
    branch text NOT NULL,
    cluster uuid,
    namespace text NOT NULL,
    revision text,
    synced_at timestamptz,
    sync text NOT NULL DEFAULT 'out_of_sync' CHECK (sync IN ('synced', 'out_of_sync')),
    health text NOT NULL DEFAULT 'unknown' CHECK (health IN ('healthy', 'progressing', 'unknown', 'missing', 'degraded')),
    paused boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    version bigint NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX environments_live ON environments (target_id, branch) WHERE deleted_at IS NULL;
-- One namespace, one environment: two live environments never share a cluster's namespace, whatever their
-- organizations.
CREATE UNIQUE INDEX environments_namespace
    ON environments (COALESCE(cluster, '00000000-0000-0000-0000-000000000000'::uuid), namespace) WHERE deleted_at IS NULL;
CREATE INDEX environments_repo ON environments (repo_id, branch) WHERE deleted_at IS NULL;
CREATE TRIGGER bump_version BEFORE UPDATE ON environments FOR EACH ROW EXECUTE FUNCTION bump_version();

-- The last delivery number each repository gave.
CREATE TABLE delivery_numbers (
    repo_id uuid PRIMARY KEY REFERENCES repositories (id) ON DELETE CASCADE,
    last bigint NOT NULL
);

-- A delivery of an environment: of a commit (deliver), of its last render again (resync), or of nothing, deleting what
-- it holds (teardown); what queued it, and where it stands. History the controller writes, so not audited; events
-- record who started or redelivered one. Each change bumps its version, its ETag.
CREATE TABLE deliveries (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    repo_id uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    number bigint NOT NULL,
    environment_id uuid NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('deliver', 'resync', 'teardown')),
    sha text,
    trigger text NOT NULL CHECK (trigger IN ('push', 'redeliver', 'resume')),
    push_id uuid REFERENCES pushes (id) ON DELETE SET NULL,
    state text NOT NULL CHECK (state IN ('waiting', 'queued', 'rendering', 'applying', 'succeeded', 'failed', 'cancelled')),
    reason text NOT NULL DEFAULT '',
    started_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    lease_until timestamptz,
    queued_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    ended_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (repo_id, number),
    FOREIGN KEY (repo_id, sha) REFERENCES commits (repo_id, sha) ON DELETE CASCADE,
    CHECK ((sha IS NULL) = (kind = 'teardown'))
);
CREATE INDEX deliveries_active ON deliveries (environment_id) WHERE state IN ('waiting', 'queued', 'rendering', 'applying');
CREATE INDEX deliveries_environment ON deliveries (environment_id, number);
CREATE INDEX deliveries_queued ON deliveries (queued_at) WHERE state = 'queued';
CREATE TRIGGER bump_version BEFORE UPDATE ON deliveries FOR EACH ROW EXECUTE FUNCTION bump_version();

-- delivery.run joins the built-in roles that hold jobs.run: Owner, Developer and Maintainer. Appended, never set, so
-- other slices' additions stay.
UPDATE roles SET permissions = permissions || '{delivery.run}'
WHERE id IN ('00000000-0000-7000-8000-000000000201', '00000000-0000-7000-8000-000000000302', '00000000-0000-7000-8000-000000000303')
  AND NOT permissions @> '{delivery.run}';

-- +goose Down
-- Rolling back forgets every environment, delivery and target principal; the audit log keeps what the targets did,
-- and what was delivered stays in its clusters.
UPDATE roles SET permissions = array_remove(permissions, 'delivery.run') WHERE built_in;
DROP TABLE deliveries;
DROP TABLE delivery_numbers;
DROP TABLE environments;
DROP TABLE delivery_targets;
DELETE FROM principals WHERE kind = 'target';
ALTER TABLE principals DROP CONSTRAINT principals_org_kind;
ALTER TABLE principals ADD CONSTRAINT principals_org_kind CHECK ((kind IN ('api_key', 'job', 'agent')) = (org_id IS NOT NULL));
ALTER TABLE principals DROP CONSTRAINT principals_kind_check;
ALTER TABLE principals ADD CONSTRAINT principals_kind_check
    CHECK (kind IN ('user', 'anonymous', 'agent', 'automation', 'installation', 'api_key', 'job'));
