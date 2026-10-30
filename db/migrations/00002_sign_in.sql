-- Sign-in: principals and the emails they hold, their identities at the providers, the flows a sign-in waits in, the
-- sessions it makes, the roles and grants a sign-in gives, and the audit log. Every ID is a UUIDv7
-- (https://docs.dev.kfirs.com/cobranch/designs/sign-in-and-organizations.md, "Data model").

-- +goose Up

-- Every actor: a user, an agent, an automation, the installation, or Anonymous, the one built-in row.
CREATE TABLE principals (
    id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('user', 'anonymous', 'agent', 'automation', 'installation')),
    display_name text NOT NULL,
    suspended boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO principals (id, kind, display_name) VALUES ('00000000-0000-7000-8000-000000000001', 'anonymous', 'Anonymous');

-- The emails a user holds, one row each, so a user may hold several: a verified email belongs to one user.
CREATE TABLE principal_emails (
    email text PRIMARY KEY CHECK (email = lower(email)),
    principal_id uuid NOT NULL REFERENCES principals (id) ON DELETE CASCADE,
    verified boolean NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX principal_emails_principal ON principal_emails (principal_id);

-- A user at an identity provider: the issuer and the subject it gave are the key, never the email.
CREATE TABLE identities (
    issuer text NOT NULL,
    subject text NOT NULL,
    principal_id uuid NOT NULL REFERENCES principals (id) ON DELETE CASCADE,
    email text NOT NULL DEFAULT '',
    email_verified boolean NOT NULL DEFAULT false,
    name text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    last_sign_in_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (issuer, subject)
);
CREATE INDEX identities_principal ON identities (principal_id);

-- A sign-in that began and waits for the provider's answer; the callback deletes it, whichever replica it reaches.
CREATE TABLE sign_in_flows (
    state_hash bytea PRIMARY KEY,
    browser_hash bytea NOT NULL,
    provider_id text NOT NULL,
    nonce text NOT NULL,
    pkce_verifier text NOT NULL,
    return_path text NOT NULL,
    expires_at timestamptz NOT NULL
);
CREATE INDEX sign_in_flows_expires ON sign_in_flows (expires_at);

-- A signed-in browser. The cookie carries the token; the row keeps its hash.
CREATE TABLE sessions (
    id uuid PRIMARY KEY,
    token_hash bytea NOT NULL UNIQUE,
    principal_id uuid NOT NULL REFERENCES principals (id) ON DELETE CASCADE,
    issuer text NOT NULL DEFAULT '',
    subject text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    idle_expires_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    user_agent text NOT NULL DEFAULT '',
    ip text NOT NULL DEFAULT ''
);
CREATE INDEX sessions_principal ON sessions (principal_id);
CREATE INDEX sessions_expires ON sessions (LEAST(idle_expires_at, expires_at));

-- A role names permissions at a scope. The built-in roles come here, at fixed IDs; custom roles come with their slice.
CREATE TABLE roles (
    id uuid PRIMARY KEY,
    scope text NOT NULL CHECK (scope IN ('installation', 'organization', 'repository')),
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    permissions text[] NOT NULL,
    built_in boolean NOT NULL DEFAULT false,
    UNIQUE (scope, name)
);
INSERT INTO roles (id, scope, name, description, permissions, built_in) VALUES
    ('00000000-0000-7000-8000-000000000101', 'installation', 'Installation admin', 'Runs the installation: its settings, its users and every organization.',
     '{installation.manage,installation.users.manage,orgs.create}', true),
    ('00000000-0000-7000-8000-000000000102', 'installation', 'Installation user', 'Creates organizations.',
     '{orgs.create}', true),
    ('00000000-0000-7000-8000-000000000201', 'organization', 'Owner', 'Everything in the organization.',
     '{org.read,org.manage,org.members.manage,org.grants.manage,org.principals.manage,org.audit.read,agents.create,agents.assign,repos.create,repo.read,repo.push,repo.merge,repo.bypass,repo.manage,annotations.write,annotations.delete_any,reviews.submit,messages.post,messages.moderate,jobs.run,secrets.manage,secrets.read}', true),
    ('00000000-0000-7000-8000-000000000202', 'organization', 'Member', 'Sees the organization, creates repositories and agents.',
     '{org.read,repos.create,agents.create,agents.assign}', true),
    ('00000000-0000-7000-8000-000000000203', 'organization', 'Guest', 'Sees the organization alone; repositories by their own grants.',
     '{org.read}', true),
    ('00000000-0000-7000-8000-000000000301', 'repository', 'Reader', 'Reads the repository.',
     '{repo.read}', true),
    ('00000000-0000-7000-8000-000000000302', 'repository', 'Developer', 'Pushes, merges, reviews, posts and runs jobs.',
     '{repo.read,repo.push,repo.merge,annotations.write,reviews.submit,messages.post,jobs.run}', true),
    ('00000000-0000-7000-8000-000000000303', 'repository', 'Maintainer', 'A developer who also manages the repository and its secrets, bypasses its rules and moderates.',
     '{repo.read,repo.push,repo.merge,repo.bypass,repo.manage,annotations.write,annotations.delete_any,reviews.submit,messages.post,messages.moderate,jobs.run,secrets.manage,secrets.read}', true),
    ('00000000-0000-7000-8000-000000000304', 'repository', 'Automation', 'Reads, annotates, reviews and posts, as a job does.',
     '{repo.read,annotations.write,reviews.submit,messages.post}', true);

-- A role given to a principal at a scope: the installation (no scope_id), or an organization or repository by ID. The
-- source says what made it, so a declarative source takes back only what it gave.
CREATE TABLE grants (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    principal_id uuid NOT NULL REFERENCES principals (id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    scope text NOT NULL CHECK (scope IN ('installation', 'organization', 'repository')),
    scope_id uuid,
    source text NOT NULL CHECK (source IN ('grant', 'admins', 'self_serve')),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((scope = 'installation') = (scope_id IS NULL)),
    UNIQUE NULLS NOT DISTINCT (principal_id, role_id, scope, scope_id)
);
CREATE INDEX grants_scope ON grants (scope, scope_id);

-- The audit log: who did what to what, when. Rows are only ever added; seq orders those of one transaction.
CREATE TABLE events (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    seq bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    at timestamptz NOT NULL DEFAULT now(),
    actor_id uuid NOT NULL REFERENCES principals (id),
    kind text NOT NULL,
    subject_id uuid,
    details jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX events_subject ON events (subject_id, at);

-- +goose Down
DROP TABLE events;
DROP TABLE grants;
DROP TABLE roles;
DROP TABLE sessions;
DROP TABLE sign_in_flows;
DROP TABLE identities;
DROP TABLE principal_emails;
DROP TABLE principals;
