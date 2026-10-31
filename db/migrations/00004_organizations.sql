-- Organizations: the organizations users belong to, their slugs (the current one and the old ones a rename keeps as
-- redirects), their memberships, the invitations and join requests that make members, and the organization an event
-- belongs to (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/sign-in-and-organizations.md,
-- "Organizations"). Every row an organization owns carries its org_id.
-- +goose Up

-- An organization: a slug for its URLs, a name, and its settings. membership_role_id is the role everyone who joins
-- gets; providers lists the identity providers its members may use, or NULL for all of the installation's.
CREATE TABLE organizations (
    id uuid PRIMARY KEY,
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    membership_role_id uuid NOT NULL REFERENCES roles (id),
    accepts_join_requests boolean NOT NULL DEFAULT true,
    providers text[],
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Every slug an organization has had, the current one included: a slug is unique among organizations and their old
-- slugs, and an old one redirects to its organization.
CREATE TABLE organization_slugs (
    slug text PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX organization_slugs_org ON organization_slugs (org_id);

-- A user's membership of an organization. What they may do there is their grants' say.
CREATE TABLE memberships (
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES principals (id) ON DELETE CASCADE,
    joined_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX memberships_user ON memberships (user_id);

-- An invitation names an email; only a user who holds it, verified, accepts it. One pending invitation per email and
-- organization; a closed one keeps who decided it.
CREATE TABLE invitations (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    email text NOT NULL CHECK (email = lower(email)),
    role_id uuid NOT NULL REFERENCES roles (id),
    invited_by uuid NOT NULL REFERENCES principals (id),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'accepted', 'declined', 'cancelled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    decided_at timestamptz,
    decided_by uuid REFERENCES principals (id)
);
CREATE UNIQUE INDEX invitations_pending ON invitations (org_id, email) WHERE state = 'pending';
CREATE INDEX invitations_pending_email ON invitations (email) WHERE state = 'pending';

-- A user's request to join an organization, waiting for one of its member managers. One pending request per user
-- and organization.
CREATE TABLE join_requests (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES principals (id) ON DELETE CASCADE,
    note text NOT NULL DEFAULT '',
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'approved', 'declined')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    decided_at timestamptz,
    decided_by uuid REFERENCES principals (id)
);
CREATE UNIQUE INDEX join_requests_pending ON join_requests (org_id, user_id) WHERE state = 'pending';
CREATE INDEX join_requests_pending_user ON join_requests (user_id) WHERE state = 'pending';

-- The organization an event happened in, for its audit trail; NULL for the installation's own.
ALTER TABLE events ADD COLUMN org_id uuid;
CREATE INDEX events_org ON events (org_id, at);

CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON organizations FOR EACH ROW EXECUTE FUNCTION audit();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON organization_slugs FOR EACH ROW EXECUTE FUNCTION audit();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON memberships FOR EACH ROW EXECUTE FUNCTION audit();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON invitations FOR EACH ROW EXECUTE FUNCTION audit();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON join_requests FOR EACH ROW EXECUTE FUNCTION audit();

-- +goose Down
DROP INDEX events_org;
ALTER TABLE events DROP COLUMN org_id;
DROP TABLE join_requests;
DROP TABLE invitations;
DROP TABLE memberships;
DROP TABLE organization_slugs;
DROP TABLE organizations;
