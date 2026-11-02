-- Jobs: what /cobranch.yaml declares at each branch tip, the principal of each job, and the runs that events queue
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/jobs.md). The Git server records the file
-- at every tip a push moves; the push worker matches its jobs against the push and queues their runs.
-- +goose Up

-- A job is a principal of its repository's organization, as an API key is; so will an agent be.
ALTER TABLE principals DROP CONSTRAINT principals_kind_check;
ALTER TABLE principals ADD CONSTRAINT principals_kind_check
    CHECK (kind IN ('user', 'anonymous', 'agent', 'automation', 'installation', 'api_key', 'job'));
ALTER TABLE principals DROP CONSTRAINT principals_org_kind;
ALTER TABLE principals ADD CONSTRAINT principals_org_kind CHECK ((kind IN ('api_key', 'job', 'agent')) = (org_id IS NOT NULL));

-- A /cobranch.yaml a push brought to a branch tip, by its object's name: what it says, as package code's Settings
-- encode it, or why it doesn't parse. Derived from Git's objects, so never changed and not audited.
CREATE TABLE settings_files (
    repo_id uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    blob text NOT NULL,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    settings jsonb,
    error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repo_id, blob),
    CHECK ((settings IS NULL) = (error <> ''))
);

-- The /cobranch.yaml at a branch's head; null without one, or for a head no push recorded it of.
ALTER TABLE branches ADD COLUMN settings_blob text;
ALTER TABLE branches ADD CONSTRAINT branches_settings FOREIGN KEY (repo_id, settings_blob) REFERENCES settings_files (repo_id, blob);

-- What triggers saw of a push's tags: the commit each tag it created or moved names, {"refs/tags/v1": "<sha>"}.
ALTER TABLE pushes ADD COLUMN peeled jsonb NOT NULL DEFAULT '{}';

-- A job of a repository, by name, is a principal of its own, kept across runs and made on its first. A renamed job is
-- a new principal; the old one keeps its history.
CREATE TABLE jobs (
    principal_id uuid PRIMARY KEY REFERENCES principals (id) ON DELETE CASCADE,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    repo_id uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    name text NOT NULL,
    UNIQUE (repo_id, name)
);

-- The last run number each repository gave.
CREATE TABLE run_numbers (
    repo_id uuid PRIMARY KEY REFERENCES repositories (id) ON DELETE CASCADE,
    last bigint NOT NULL
);

-- A run of a job at a commit and ref: the event that queued it, the job's definition then, and where it stands. Runs
-- are history the controller writes, not data people manage, so they aren't audited; events record who started or
-- cancelled one. Each change bumps its version, its ETag.
CREATE TABLE job_runs (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    repo_id uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    number bigint NOT NULL,
    job_id uuid NOT NULL REFERENCES jobs (principal_id) ON DELETE CASCADE,
    sha text NOT NULL,
    ref text NOT NULL,
    trigger jsonb NOT NULL,
    cause_run_id uuid REFERENCES job_runs (id) ON DELETE SET NULL,
    definition jsonb NOT NULL,
    state text NOT NULL CHECK (state IN ('waiting', 'queued', 'running', 'succeeded', 'failed', 'cancelled')),
    reason text NOT NULL DEFAULT '',
    started_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    lease_until timestamptz,
    queued_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    ended_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (repo_id, number),
    FOREIGN KEY (repo_id, sha) REFERENCES commits (repo_id, sha) ON DELETE CASCADE
);
CREATE INDEX job_runs_active ON job_runs (job_id, ref) WHERE state IN ('waiting', 'queued', 'running');
CREATE INDEX job_runs_commit ON job_runs (repo_id, sha);
CREATE INDEX job_runs_queued ON job_runs (queued_at) WHERE state = 'queued';
CREATE TRIGGER bump_version BEFORE UPDATE ON job_runs FOR EACH ROW EXECUTE FUNCTION bump_version();

-- +goose Down
-- Rolling back forgets every run, recorded file and job principal; the audit log keeps what the jobs did.
DROP TABLE job_runs;
DROP TABLE run_numbers;
DROP TABLE jobs;
ALTER TABLE pushes DROP COLUMN peeled;
ALTER TABLE branches DROP CONSTRAINT branches_settings;
ALTER TABLE branches DROP COLUMN settings_blob;
DROP TABLE settings_files;
DELETE FROM principals WHERE kind = 'job';
ALTER TABLE principals DROP CONSTRAINT principals_org_kind;
ALTER TABLE principals ADD CONSTRAINT principals_org_kind CHECK ((kind = 'api_key') = (org_id IS NOT NULL));
ALTER TABLE principals DROP CONSTRAINT principals_kind_check;
ALTER TABLE principals ADD CONSTRAINT principals_kind_check
    CHECK (kind IN ('user', 'anonymous', 'agent', 'automation', 'installation', 'api_key'));
