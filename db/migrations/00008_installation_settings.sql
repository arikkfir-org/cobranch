-- The installation's settings that the Back-office changes: who may sign up and whether new users create organizations
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/back-office.md, "Installation settings").
-- One row. A NULL setting is unset and takes its default; a setting the chart's values give wins over this row's. Every
-- change bumps version, which a change names to refuse one made meanwhile, and is audited.
-- +goose Up
CREATE TABLE installation_settings (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    sign_up text CHECK (sign_up IN ('anyone', 'domains', 'invited')),
    sign_up_domains text[] NOT NULL DEFAULT '{}',
    self_serve boolean,
    version bigint NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    CHECK (sign_up IS DISTINCT FROM 'domains' OR cardinality(sign_up_domains) > 0)
);
INSERT INTO installation_settings DEFAULT VALUES;

-- +goose StatementBegin
-- bump_version counts an updated row's changes in its version, which is its ETag.
CREATE FUNCTION bump_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.version := OLD.version + 1;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER bump_version BEFORE UPDATE ON installation_settings FOR EACH ROW EXECUTE FUNCTION bump_version();
CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON installation_settings FOR EACH ROW EXECUTE FUNCTION audit();

-- +goose Down
DROP TABLE installation_settings;
DROP FUNCTION bump_version();
