-- API keys: a principal of an organization, with the secrets that act as it. A key's secret is shown once; the row
-- keeps its hash and its last four characters. A key holds at most two live secrets, so a rotation overlaps them
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/sign-in-and-organizations.md, "API keys").

-- +goose Up

-- An API key is a principal of kind api_key that belongs to an organization and goes with it; a user belongs to none.
ALTER TABLE principals DROP CONSTRAINT principals_kind_check;
ALTER TABLE principals ADD CONSTRAINT principals_kind_check
    CHECK (kind IN ('user', 'anonymous', 'agent', 'automation', 'installation', 'api_key'));
ALTER TABLE principals ADD COLUMN org_id uuid REFERENCES organizations (id) ON DELETE CASCADE;
ALTER TABLE principals ADD COLUMN created_by uuid REFERENCES principals (id) ON DELETE SET NULL;
ALTER TABLE principals ADD CONSTRAINT principals_org_kind CHECK ((kind = 'api_key') = (org_id IS NOT NULL));
CREATE INDEX principals_org ON principals (org_id, display_name) WHERE org_id IS NOT NULL;

-- A credential that acts as a principal: an API key's secret for now, hashed. A null expires_at never expires.
CREATE TABLE credentials (
    id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('api_key')),
    token_hash bytea NOT NULL UNIQUE,
    principal_id uuid NOT NULL REFERENCES principals (id) ON DELETE CASCADE,
    hint text NOT NULL,
    created_at timestamptz NOT NULL,
    created_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    expires_at timestamptz,
    last_used_at timestamptz,
    revoked_at timestamptz
);
CREATE INDEX credentials_principal ON credentials (principal_id, created_at);
-- The hash never reaches the log, and a secret's mere use (last_used_at) is no change worth a row.
CREATE TRIGGER audit AFTER INSERT OR UPDATE OF revoked_at OR DELETE ON credentials
    FOR EACH ROW EXECUTE FUNCTION audit('token_hash');

-- A key acts, so rows may name it, and deleting the key deletes its principal: its events keep its ID (the log names
-- no principal row any more), and an invitation or a decision it made loses its author, so an invitation of a deleted
-- key adds nobody, as one of a manager who lost the permission does.
ALTER TABLE events DROP CONSTRAINT events_actor_id_fkey;
ALTER TABLE invitations ALTER COLUMN invited_by DROP NOT NULL;
ALTER TABLE invitations DROP CONSTRAINT invitations_invited_by_fkey;
ALTER TABLE invitations ADD CONSTRAINT invitations_invited_by_fkey
    FOREIGN KEY (invited_by) REFERENCES principals (id) ON DELETE SET NULL;
ALTER TABLE invitations DROP CONSTRAINT invitations_decided_by_fkey;
ALTER TABLE invitations ADD CONSTRAINT invitations_decided_by_fkey
    FOREIGN KEY (decided_by) REFERENCES principals (id) ON DELETE SET NULL;
ALTER TABLE join_requests DROP CONSTRAINT join_requests_decided_by_fkey;
ALTER TABLE join_requests ADD CONSTRAINT join_requests_decided_by_fkey
    FOREIGN KEY (decided_by) REFERENCES principals (id) ON DELETE SET NULL;

-- +goose Down
-- Rolling back deletes the API keys, with their secrets and grants: the previous schema has no room for them. Rows a
-- key authored keep no author, and events keep their actor as before, with the key constraint back for the rest.
DELETE FROM principals WHERE kind = 'api_key';
DROP TABLE credentials;
ALTER TABLE join_requests DROP CONSTRAINT join_requests_decided_by_fkey;
ALTER TABLE join_requests ADD CONSTRAINT join_requests_decided_by_fkey FOREIGN KEY (decided_by) REFERENCES principals (id);
ALTER TABLE invitations DROP CONSTRAINT invitations_decided_by_fkey;
ALTER TABLE invitations ADD CONSTRAINT invitations_decided_by_fkey FOREIGN KEY (decided_by) REFERENCES principals (id);
ALTER TABLE invitations DROP CONSTRAINT invitations_invited_by_fkey;
DELETE FROM invitations WHERE invited_by IS NULL;
ALTER TABLE invitations ALTER COLUMN invited_by SET NOT NULL;
ALTER TABLE invitations ADD CONSTRAINT invitations_invited_by_fkey FOREIGN KEY (invited_by) REFERENCES principals (id);
DELETE FROM events WHERE actor_id NOT IN (SELECT id FROM principals);
ALTER TABLE events ADD CONSTRAINT events_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES principals (id);
DROP INDEX principals_org;
ALTER TABLE principals DROP CONSTRAINT principals_org_kind;
ALTER TABLE principals DROP COLUMN created_by;
ALTER TABLE principals DROP COLUMN org_id;
ALTER TABLE principals DROP CONSTRAINT principals_kind_check;
ALTER TABLE principals ADD CONSTRAINT principals_kind_check
    CHECK (kind IN ('user', 'anonymous', 'agent', 'automation', 'installation'));
