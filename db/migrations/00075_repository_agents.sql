-- Repository agents: an agent that its repository's main /cobranch.yaml declares, a principal of the repository that
-- does there what the file lists within main's ceiling, and is retired once the file no longer declares it
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/repo-agents.md, "Repository agents").
-- +goose Up

-- The repository whose main file declares the agent, null for an organization's; the repository permissions it
-- declares, which hold on that repository alone; the line of its name in the file; and when the file stopped declaring
-- it, null while it does. Only a repository's agent has any of them, and none is built in.
ALTER TABLE agents
    ADD COLUMN repo_id uuid REFERENCES repositories (id) ON DELETE CASCADE,
    ADD COLUMN permissions text[] NOT NULL DEFAULT '{}',
    ADD COLUMN line integer NOT NULL DEFAULT 0 CHECK (line >= 0),
    ADD COLUMN retired_at timestamptz,
    ADD CONSTRAINT agents_declared CHECK (repo_id IS NOT NULL OR (cardinality(permissions) = 0 AND line = 0 AND retired_at IS NULL)),
    ADD CONSTRAINT agents_declared_built_in CHECK (repo_id IS NULL OR NOT built_in);

-- Names are unique among the organization's own agents and among each repository's, whatever their case: a
-- repository's agent may share a name with one of the organization's, which a job naming it finds first.
DROP INDEX agents_name;
CREATE UNIQUE INDEX agents_name ON agents (org_id, lower(name)) WHERE repo_id IS NULL;
CREATE UNIQUE INDEX agents_repository_name ON agents (repo_id, lower(name)) WHERE repo_id IS NOT NULL;

-- A change to a repository's agent wakes the repository's pages too.
CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON agents
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id', 'repo_id');

-- +goose Down
-- Rolling back deletes the repositories' agents, with their tasks and grants; the audit log keeps what they did.
CREATE OR REPLACE TRIGGER notify_changes AFTER INSERT OR UPDATE OR DELETE ON agents
    FOR EACH ROW EXECUTE FUNCTION notify_changes('org_id');
DELETE FROM principals WHERE id IN (SELECT principal_id FROM agents WHERE repo_id IS NOT NULL);
DROP INDEX agents_repository_name;
DROP INDEX agents_name;
CREATE UNIQUE INDEX agents_name ON agents (org_id, lower(name));
ALTER TABLE agents DROP CONSTRAINT agents_declared_built_in, DROP CONSTRAINT agents_declared, DROP COLUMN retired_at,
    DROP COLUMN line, DROP COLUMN permissions, DROP COLUMN repo_id;
