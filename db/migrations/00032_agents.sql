-- Agents and their tasks: an agent is a principal its organization owns, and a task is work a principal assigns it,
-- which does no more than both of them may (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md,
-- "Agents" and "Tasks"). Nothing runs a task yet: tasks wait Queued until the controller runs them.
-- +goose Up

-- An agent of an organization, by the principal of kind agent it is: what its creator calls it, a description, and the
-- standing instructions every task of it starts from. Its creator, or a principal holding org.principals.manage,
-- changes them; each change bumps its version, which is its ETag. A built-in agent, which Cobranch makes for every
-- organization, is never deleted. Names are unique in the organization, whatever their case.
CREATE TABLE agents (
    principal_id uuid PRIMARY KEY REFERENCES principals (id) ON DELETE CASCADE,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 1000),
    instructions text NOT NULL DEFAULT '' CHECK (char_length(instructions) <= 20000),
    built_in boolean NOT NULL DEFAULT false,
    created_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX agents_name ON agents (org_id, lower(name));
CREATE INDEX agents_created_by ON agents (created_by) WHERE created_by IS NOT NULL;

-- What a task is for. Each slice that starts tasks of its own adds its kind here, only where missing, so the order its
-- migrations apply in doesn't matter; a person's assignment is 'assigned'.
CREATE TABLE task_kinds (
    kind text PRIMARY KEY
);
INSERT INTO task_kinds (kind) VALUES ('assigned');

-- The last task number each organization gave.
CREATE TABLE task_numbers (
    org_id uuid PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    last bigint NOT NULL
);

-- A task of an agent: a title and instructions in Markdown, and perhaps where it works, a repository, one of its
-- branches and a thread of that branch's chat. Its assigner caps what it does; a task Cobranch starts itself names the
-- agent as its assigner, so the agent's own grants cap it. An assigner whose principal is deleted leaves none, and a
-- task without an assigner may do nothing. Each change bumps its version, which is its ETag.
CREATE TABLE agent_tasks (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    number bigint NOT NULL,
    agent_id uuid NOT NULL REFERENCES agents (principal_id) ON DELETE CASCADE,
    kind text NOT NULL DEFAULT 'assigned' REFERENCES task_kinds (kind),
    assigned_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    instructions text NOT NULL DEFAULT '' CHECK (char_length(instructions) <= 20000),
    repo_id uuid REFERENCES repositories (id) ON DELETE CASCADE,
    branch_id uuid REFERENCES branches (id) ON DELETE CASCADE,
    thread_id uuid REFERENCES chat_threads (id) ON DELETE SET NULL,
    state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'waiting', 'succeeded', 'failed', 'cancelled')),
    reason text NOT NULL DEFAULT '',
    summary text NOT NULL DEFAULT '',
    cancelled_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    ended_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (org_id, number),
    CHECK (branch_id IS NULL OR repo_id IS NOT NULL),
    CHECK ((state IN ('succeeded', 'failed', 'cancelled')) = (ended_at IS NOT NULL))
);
CREATE INDEX agent_tasks_agent ON agent_tasks (agent_id, number);
CREATE INDEX agent_tasks_repo ON agent_tasks (repo_id) WHERE repo_id IS NOT NULL;
CREATE INDEX agent_tasks_branch ON agent_tasks (branch_id) WHERE branch_id IS NOT NULL;
CREATE INDEX agent_tasks_thread ON agent_tasks (thread_id) WHERE thread_id IS NOT NULL;
CREATE INDEX agent_tasks_assigned_by ON agent_tasks (assigned_by) WHERE assigned_by IS NOT NULL;
CREATE INDEX agent_tasks_cancelled_by ON agent_tasks (cancelled_by) WHERE cancelled_by IS NOT NULL;
CREATE INDEX agent_tasks_active ON agent_tasks (created_at) WHERE state IN ('queued', 'running', 'waiting');

CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON agents FOR EACH ROW EXECUTE FUNCTION audit();
CREATE TRIGGER bump_version BEFORE UPDATE ON agents FOR EACH ROW EXECUTE FUNCTION bump_version();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON agent_tasks FOR EACH ROW EXECUTE FUNCTION audit();
CREATE TRIGGER bump_version BEFORE UPDATE ON agent_tasks FOR EACH ROW EXECUTE FUNCTION bump_version();

-- +goose Down
-- Rolling back deletes the agents, with their tasks and grants; the audit log keeps what they did.
DROP TABLE agent_tasks;
DROP TABLE task_numbers;
DROP TABLE task_kinds;
DELETE FROM principals WHERE id IN (SELECT principal_id FROM agents);
DROP TABLE agents;
