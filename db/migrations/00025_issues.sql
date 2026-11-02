-- Issues: an organization's Linear-like issues, each named by the organization's key and a number, such as ACME-42,
-- and the permissions that reach them (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/issues.md,
-- "Issues" and "Who may do what"). Branch links and labels come in later migrations.
-- +goose Up

-- +goose StatementBegin
-- default_issue_key is the issue key an organization of that slug starts with: its first word's letters, at most five,
-- uppercased, or ISSUE when it has none.
CREATE FUNCTION default_issue_key(slug text) RETURNS text LANGUAGE sql IMMUTABLE AS $$
    SELECT coalesce(nullif(upper(left(regexp_replace(split_part(slug, '-', 1), '[^a-z]', '', 'g'), 5)), ''), 'ISSUE')
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- issue_key_from_slug gives a new organization its default key unless its insert names one.
CREATE FUNCTION issue_key_from_slug() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.issue_key := coalesce(NEW.issue_key, default_issue_key(NEW.slug));
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- The key an organization's issue identifiers start with: 1 to 10 uppercase letters and digits, starting with a
-- letter. Changing it renames every identifier; the number stays.
ALTER TABLE organizations ADD COLUMN issue_key text;
UPDATE organizations SET issue_key = default_issue_key(slug);
ALTER TABLE organizations
    ALTER COLUMN issue_key SET NOT NULL,
    ADD CONSTRAINT organizations_issue_key_check CHECK (issue_key ~ '^[A-Z][A-Z0-9]{0,9}$');
CREATE TRIGGER issue_key BEFORE INSERT ON organizations FOR EACH ROW EXECUTE FUNCTION issue_key_from_slug();

-- The last number an organization gave an issue, which the next one's transaction increments: numbers are never
-- shared or reused. Bookkeeping, so no trigger audit().
CREATE TABLE issue_numbers (
    org_id uuid PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    last integer NOT NULL
);

-- An issue: its number in its organization; a title and a Markdown description; its state, of six, closed_at set while
-- it is done or canceled; a priority, 0 for none, then urgent (1) to low (4); one assignee, a principal of the
-- organization; optionally the repository it is about; who created it; and its version, its ETag.
CREATE TABLE issues (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    number integer NOT NULL,
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    state text NOT NULL DEFAULT 'backlog' CHECK (state IN ('backlog', 'todo', 'in_progress', 'in_review', 'done', 'canceled')),
    priority smallint NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 4),
    assignee_id uuid REFERENCES principals (id) ON DELETE SET NULL,
    repo_id uuid REFERENCES repositories (id) ON DELETE SET NULL,
    created_by uuid REFERENCES principals (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    closed_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (org_id, number),
    CHECK ((state IN ('done', 'canceled')) = (closed_at IS NOT NULL))
);
CREATE INDEX issues_assignee ON issues (assignee_id);
CREATE INDEX issues_repo ON issues (repo_id);
CREATE INDEX issues_created_by ON issues (created_by);

CREATE TRIGGER audit AFTER INSERT OR UPDATE OR DELETE ON issues FOR EACH ROW EXECUTE FUNCTION audit();
CREATE TRIGGER bump_version BEFORE UPDATE ON issues FOR EACH ROW EXECUTE FUNCTION bump_version();

-- The issue permissions join the built-in roles: Owners hold all three, Members read and write, Guests none. Each
-- role gains only what it lacks, beside what other migrations give it.
UPDATE roles SET permissions = permissions || '{issues.read,issues.write,issues.manage}'
WHERE id = '00000000-0000-7000-8000-000000000201' AND NOT permissions && '{issues.read,issues.write,issues.manage}';
UPDATE roles SET permissions = permissions || '{issues.read,issues.write}'
WHERE id = '00000000-0000-7000-8000-000000000202' AND NOT permissions && '{issues.read,issues.write}';

-- +goose Down
-- Rolling back forgets every issue, and the issue permissions of every role; the audit log keeps what they were.
UPDATE roles SET permissions = array_remove(array_remove(array_remove(permissions, 'issues.read'), 'issues.write'), 'issues.manage');
DROP TABLE issues;
DROP TABLE issue_numbers;
DROP TRIGGER issue_key ON organizations;
ALTER TABLE organizations DROP COLUMN issue_key;
DROP FUNCTION issue_key_from_slug();
DROP FUNCTION default_issue_key(text);
