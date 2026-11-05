-- Preferences: how Cobranch looks to a person, kept with them so it follows them to every browser
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/design-system.md, "Colour").
-- +goose Up

-- A principal's preferences: the theme the Console draws in (light, dark, or system, which follows the operating
-- system's), and their version, their ETag. A principal without a row has the defaults, at version 0.
CREATE TABLE preferences (
    principal_id uuid PRIMARY KEY REFERENCES principals (id) ON DELETE CASCADE,
    theme text NOT NULL DEFAULT 'system' CHECK (theme IN ('system', 'light', 'dark')),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1
);

CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON preferences FOR EACH ROW EXECUTE FUNCTION audit();
CREATE TRIGGER bump_version BEFORE UPDATE ON preferences FOR EACH ROW EXECUTE FUNCTION bump_version();

-- +goose Down
-- Rolling back forgets every person's preferences, who then see the defaults; the audit log keeps them.
DROP TABLE preferences;
