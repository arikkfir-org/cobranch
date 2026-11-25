-- Row-level security: PostgreSQL itself keeps organizations apart, under the filters every query already has. Each
-- table that carries org_id gets policy organization, which shows role cobranch_tenant the rows of the organization its
-- transaction is pinned to (00063) and refuses it any other's, whatever the query's WHERE says
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/tenant-isolation.md). The role Cobranch
-- connects as owns the tables, so the policies leave it alone: that is the work that spans organizations.
--
-- A later migration that creates a table with org_id ends with SELECT isolate_organization('<table>'); a test fails
-- the build of one that doesn't.

-- +goose Up

-- +goose StatementBegin
-- isolate_organization turns on row-level security for tbl, a table with column org_id, and gives it policy
-- organization. In a shared table, whose rows without an organization belong to the installation (a person, a built-in
-- role), an organization reads those too, and still writes only its own.
CREATE FUNCTION isolate_organization(tbl regclass, shared boolean DEFAULT false) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', tbl);
    EXECUTE format('CREATE POLICY organization ON %s TO cobranch_tenant USING (%s) WITH CHECK (org_id = (SELECT current_org()))',
        tbl, CASE WHEN shared THEN 'org_id IS NULL OR org_id = (SELECT current_org())' ELSE 'org_id = (SELECT current_org())' END);
END
$$;
-- +goose StatementEnd

-- An organization's own row: its id is the organization.
ALTER TABLE organizations ENABLE ROW LEVEL SECURITY;
CREATE POLICY organization ON organizations TO cobranch_tenant
    USING (id = (SELECT current_org())) WITH CHECK (id = (SELECT current_org()));

SELECT isolate_organization('organization_slugs');
SELECT isolate_organization('memberships');
SELECT isolate_organization('invitations');
SELECT isolate_organization('join_requests');
SELECT isolate_organization('repositories');
SELECT isolate_organization('repository_slugs');
SELECT isolate_organization('refs');
SELECT isolate_organization('packs');
SELECT isolate_organization('pushes');
SELECT isolate_organization('commits');
SELECT isolate_organization('branches');
SELECT isolate_organization('annotations');
SELECT isolate_organization('merge_requirements');
SELECT isolate_organization('settings_files');
SELECT isolate_organization('jobs');
SELECT isolate_organization('job_runs');
SELECT isolate_organization('chat_threads');
SELECT isolate_organization('chat_messages');
SELECT isolate_organization('reviews');
SELECT isolate_organization('issue_numbers');
SELECT isolate_organization('issues');
SELECT isolate_organization('organization_limits');
SELECT isolate_organization('branch_issues');
SELECT isolate_organization('delivery_targets');
SELECT isolate_organization('environments');
SELECT isolate_organization('deliveries');
SELECT isolate_organization('secrets');
SELECT isolate_organization('agents');
SELECT isolate_organization('task_numbers');
SELECT isolate_organization('agent_tasks');
SELECT isolate_organization('model_providers');
SELECT isolate_organization('review_settings');
SELECT isolate_organization('labels');
SELECT isolate_organization('schedule_slots');
SELECT isolate_organization('conversations');
SELECT isolate_organization('artifacts');
SELECT isolate_organization('models');
SELECT isolate_organization('task_spend');
SELECT isolate_organization('viewed_files');
SELECT isolate_organization('principals', shared => true);
SELECT isolate_organization('roles', shared => true);
SELECT isolate_organization('events', shared => true);

-- +goose Down
DROP POLICY organization ON organizations;
ALTER TABLE organizations DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON organization_slugs;
ALTER TABLE organization_slugs DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON memberships;
ALTER TABLE memberships DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON invitations;
ALTER TABLE invitations DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON join_requests;
ALTER TABLE join_requests DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON repositories;
ALTER TABLE repositories DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON repository_slugs;
ALTER TABLE repository_slugs DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON refs;
ALTER TABLE refs DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON packs;
ALTER TABLE packs DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON pushes;
ALTER TABLE pushes DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON commits;
ALTER TABLE commits DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON branches;
ALTER TABLE branches DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON annotations;
ALTER TABLE annotations DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON merge_requirements;
ALTER TABLE merge_requirements DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON settings_files;
ALTER TABLE settings_files DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON jobs;
ALTER TABLE jobs DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON job_runs;
ALTER TABLE job_runs DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON chat_threads;
ALTER TABLE chat_threads DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON chat_messages;
ALTER TABLE chat_messages DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON reviews;
ALTER TABLE reviews DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON issue_numbers;
ALTER TABLE issue_numbers DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON issues;
ALTER TABLE issues DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON organization_limits;
ALTER TABLE organization_limits DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON branch_issues;
ALTER TABLE branch_issues DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON delivery_targets;
ALTER TABLE delivery_targets DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON environments;
ALTER TABLE environments DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON deliveries;
ALTER TABLE deliveries DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON secrets;
ALTER TABLE secrets DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON agents;
ALTER TABLE agents DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON task_numbers;
ALTER TABLE task_numbers DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON agent_tasks;
ALTER TABLE agent_tasks DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON model_providers;
ALTER TABLE model_providers DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON review_settings;
ALTER TABLE review_settings DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON labels;
ALTER TABLE labels DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON schedule_slots;
ALTER TABLE schedule_slots DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON conversations;
ALTER TABLE conversations DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON artifacts;
ALTER TABLE artifacts DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON models;
ALTER TABLE models DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON task_spend;
ALTER TABLE task_spend DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON viewed_files;
ALTER TABLE viewed_files DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON principals;
ALTER TABLE principals DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON roles;
ALTER TABLE roles DISABLE ROW LEVEL SECURITY;
DROP POLICY organization ON events;
ALTER TABLE events DISABLE ROW LEVEL SECURITY;
DROP FUNCTION isolate_organization(regclass, boolean);
