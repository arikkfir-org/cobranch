-- The identity providers the Back-office adds beside the chart's values' ones
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/back-office.md, "Identity providers").
-- A provider names an issuer or a preset, never both. Its client secret is sealed with the installation's secrets key
-- (AES-256-GCM, bound to the provider's ID) and never read back to the Back-office, nor written to the audit log.
-- Its version, which is its ETag and keys the Auth server's built provider, comes from one sequence for every row, on
-- creation and on each change: a provider removed and added again under its ID never repeats a version it had.
-- +goose Up
CREATE SEQUENCE identity_provider_versions;

CREATE TABLE identity_providers (
    id text PRIMARY KEY CHECK (id ~ '^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$'),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    issuer text CHECK (issuer ~ '^https://'),
    preset text,
    client_id text NOT NULL CHECK (length(client_id) BETWEEN 1 AND 500),
    client_secret bytea NOT NULL,
    version bigint NOT NULL DEFAULT nextval('identity_provider_versions'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    updated_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    CHECK ((issuer IS NULL) <> (preset IS NULL))
);

ALTER SEQUENCE identity_provider_versions OWNED BY identity_providers.version;

-- +goose StatementBegin
-- next_identity_provider_version gives a changed provider the sequence's next version.
CREATE FUNCTION next_identity_provider_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.version := nextval('identity_provider_versions');
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER next_version BEFORE UPDATE ON identity_providers
    FOR EACH ROW EXECUTE FUNCTION next_identity_provider_version();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON identity_providers
    FOR EACH ROW EXECUTE FUNCTION audit('client_secret');

-- +goose Down
DROP TABLE identity_providers;
DROP FUNCTION next_identity_provider_version();
