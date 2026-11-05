-- The Console's Artifacts page takes the slug artifacts under an organization
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/artifacts.md, "The Console").
-- +goose Up

-- A repository that had the slug moves to artifacts-repository, or the first of artifacts-repository-2, -3 and on that
-- its organization has free. Its old slug stays among its slugs, so its API and Git URLs still lead to it.
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    candidate text;
    n int;
BEGIN
    FOR r IN SELECT id, org_id, slug FROM repositories WHERE slug = 'artifacts' LOOP
        candidate := r.slug || '-repository';
        n := 1;
        WHILE EXISTS (SELECT 1 FROM repositories WHERE org_id = r.org_id AND slug = candidate) LOOP
            n := n + 1;
            candidate := r.slug || '-repository-' || n;
        END LOOP;
        UPDATE repositories SET slug = candidate WHERE id = r.id;
        INSERT INTO repository_slugs (org_id, slug, repo_id) VALUES (r.org_id, candidate, r.id)
        ON CONFLICT (org_id, slug) DO UPDATE SET repo_id = EXCLUDED.repo_id, created_at = now();
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- A moved repository keeps its new slug, and its old one still leads to it.
SELECT 1;
