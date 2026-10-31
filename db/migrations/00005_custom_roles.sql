-- Custom roles: a role an organization defines for itself, beside the built-in ones
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/sign-in-and-organizations.md, "Roles and
-- grants"). A custom role carries its organization's org_id and is granted there alone; a built-in role carries none.
-- +goose Up

ALTER TABLE roles ADD COLUMN org_id uuid REFERENCES organizations (id) ON DELETE CASCADE;
ALTER TABLE roles ADD CONSTRAINT roles_org_or_built_in CHECK (built_in = (org_id IS NULL));
ALTER TABLE roles DROP CONSTRAINT roles_scope_name_key;
-- A name is unique among the built-in roles of a scope, and among one organization's custom roles.
CREATE UNIQUE INDEX roles_name ON roles (org_id, scope, name) NULLS NOT DISTINCT;
CREATE INDEX roles_org ON roles (org_id);

-- An invitation keeps the ID of the role it offered, as events do, even once a custom role is gone: deleting a role
-- cancels the invitations that still offer it.
ALTER TABLE invitations DROP CONSTRAINT invitations_role_id_fkey;

-- +goose Down
ALTER TABLE invitations ADD CONSTRAINT invitations_role_id_fkey FOREIGN KEY (role_id) REFERENCES roles (id);
DROP INDEX roles_org;
DROP INDEX roles_name;
ALTER TABLE roles ADD CONSTRAINT roles_scope_name_key UNIQUE (scope, name);
ALTER TABLE roles DROP CONSTRAINT roles_org_or_built_in;
ALTER TABLE roles DROP COLUMN org_id;
